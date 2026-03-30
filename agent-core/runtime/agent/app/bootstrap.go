package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/events"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/traffic"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/quicbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
)

// BootstrapOptions 定义 runtime 初始化时的可选覆盖项。
type BootstrapOptions struct {
	// TunnelPoolOverride 允许外部按字段覆盖 tunnelPool 参数。
	// 首版仅在 Bootstrap 时生效，不支持运行时热更新。
	TunnelPoolOverride *TunnelPoolOverride
}

// RunOptions 定义 runtime 运行时需要拉起的本地管理入口。
type RunOptions struct {
	EnableLocalRPC bool
	EnableWeb      bool
}

// Validate 校验运行模式与当前配置是否匹配。
func (options RunOptions) Validate(config Config) error {
	if !options.EnableLocalRPC && !options.EnableWeb {
		return errors.New("no serve target enabled: pass -tauri or enable ui.web / pass -web")
	}
	if options.EnableWeb && !config.UI.Web.Enabled {
		return errors.New("web mode requires ui.web.enabled=true in config")
	}
	return nil
}

// Runtime wires the agent runtime subsystems together.
type Runtime struct {
	cfg Config

	startedAt time.Time

	bridgeMu           sync.RWMutex
	bridgeDesiredUp    bool
	bridgeState        string
	bridgeSession      string
	bridgeEpoch        uint64
	bridgeSessionReady bool
	reconnects         uint64
	heartbeatAt        time.Time
	heartbeatSentAt    time.Time
	updatedAt          time.Time
	lastErr            string
	retryFailStreak    uint32
	retryBackoff       time.Duration
	nextRetryAt        time.Time
	tunnelIDSequence   uint64
	bridgeCommandChan  chan bridgeCommand

	controlChannel     transport.ControlChannel
	tcpTransport       *tcpbinding.Transport
	grpcTransport      *grpcbinding.Transport
	quicTransport      *quicbinding.Transport
	grpcClient         transportgen.GRPCH2TransportServiceClient
	grpcConn           *grpc.ClientConn
	quicConn           *quicbinding.Conn
	quicTunnelProducer *quicbinding.TunnelProducer
	tunnelRegistry     *tunnel.Registry
	tunnelManager      *tunnel.Manager
	refillHandler      *control.RefillHandler
	serviceCatalog     *service.Catalog
	controlPublisher   *control.Publisher
	tunnelReporter     *control.TunnelReporter
	healthReporter     *control.HealthReporter
	trafficAcceptor    *traffic.Acceptor
	trafficOpener      *traffic.Opener

	trafficWakeupChannel chan struct{}
	trafficWorkersMutex  sync.Mutex
	trafficWorkers       map[string]struct{}
	tunnelAssocMutex     sync.RWMutex
	tunnelAssociations   map[string]tunnelAssociation
	trafficStatsMutex    sync.Mutex
	trafficStatsLastAt   time.Time
	trafficUploadLast    uint64
	trafficDownloadLast  uint64
	diagnoseMu           sync.RWMutex
	diagnoseEvents       []runtimeDiagnoseEvent
	diagnoseUpdatedAt    time.Time
	metrics              *obs.Metrics
	configStore          *agentRuntimeConfigStore

	ipcServer  *localRPCServer
	httpServer *httpAgentServer
	shutdownCh chan struct{}
	shutdownMu sync.Mutex
	stopped    bool
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	return BootstrapWithOptions(ctx, cfg, BootstrapOptions{})
}

// BootstrapWithOptions 在基础配置之上应用初始化覆盖参数并完成校验。
func BootstrapWithOptions(ctx context.Context, cfg Config, options BootstrapOptions) (*Runtime, error) {
	resolvedConfig := cfg
	if options.TunnelPoolOverride != nil {
		// 仅覆盖显式传入字段，未传字段保持原配置（通常来自默认值）。
		resolvedConfig = resolvedConfig.ApplyTunnelPoolOverride(*options.TunnelPoolOverride)
	}
	if err := resolvedConfig.Validate(); err != nil {
		return nil, err
	}
	serviceCatalog := service.NewCatalog()
	return &Runtime{
		cfg:                resolvedConfig,
		bridgeDesiredUp:    true,
		bridgeState:        events.BridgeStateConnecting,
		updatedAt:          time.Now().UTC(),
		bridgeCommandChan:  make(chan bridgeCommand, 8),
		serviceCatalog:     serviceCatalog,
		controlPublisher:   control.NewPublisher("", 0, 0),
		healthReporter:     control.NewHealthReporter(control.HealthReporterOptions{}),
		tunnelAssociations: make(map[string]tunnelAssociation),
		diagnoseEvents:     make([]runtimeDiagnoseEvent, 0, runtimeDiagnoseEventBufferSize),
		metrics:            obs.NewMetrics(),
		configStore:        newAgentRuntimeConfigStore(resolvedConfig),
		shutdownCh:         make(chan struct{}),
	}, nil
}

// Run 根据运行选项启动 LocalRPC / HTTP 管理面。
func (r *Runtime) Run(ctx context.Context, options RunOptions) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	if err := options.Validate(r.cfg); err != nil {
		return err
	}
	r.startedAt = time.Now().UTC()
	log.Printf(
		"agent runtime starting agent_id=%s bridge_addr=%s bridge_transport=%s",
		r.cfg.AgentID,
		r.cfg.BridgeAddr,
		r.cfg.BridgeTransport,
	)

	if err := r.initTransport(); err != nil {
		return err
	}
	if err := r.initTunnelManager(); err != nil {
		return err
	}
	if err := r.initTrafficRuntime(); err != nil {
		return err
	}
	ipcServer, httpServer, err := r.resolveRuntimeServers(options)
	if err != nil {
		return err
	}
	r.shutdownMu.Lock()
	r.ipcServer = ipcServer
	r.httpServer = httpServer
	r.shutdownMu.Unlock()

	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go func() {
		select {
		case <-ctx.Done():
			cancelRun()
		case <-r.shutdownCh:
			cancelRun()
		}
	}()

	bridgeErrChan := make(chan error, 1)
	go func() {
		bridgeErrChan <- r.runBridgeControlLoop(runContext)
	}()

	tunnelErrChan := make(chan error, 1)
	go func() {
		tunnelErrChan <- r.tunnelManager.Start(runContext)
	}()
	if r.tunnelReporter != nil {
		go func() {
			// reporter 发送失败不应导致 runtime 退出，后续周期会继续纠偏。
			_ = r.tunnelReporter.Run(runContext)
		}()
	}

	trafficErrChan := make(chan error, 1)
	go func() {
		trafficErrChan <- r.runTrafficAcceptorLoop(runContext)
	}()

	var serverErrChan chan error
	if ipcServer != nil {
		serverErrChan = make(chan error, 1)
		go func() {
			serverErrChan <- ipcServer.Serve(runContext)
		}()
	}

	var httpErrChan chan error
	if httpServer != nil {
		httpErrChan = make(chan error, 1)
		go func() {
			httpErrChan <- httpServer.Serve(runContext)
		}()
	}

	select {
	case <-runContext.Done():
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		return nil
	case serverErr := <-serverErrChan:
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if errors.Is(serverErr, context.Canceled) {
			return nil
		}
		return serverErr
	case httpErr := <-httpErrChan:
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if errors.Is(httpErr, context.Canceled) || errors.Is(httpErr, http.ErrServerClosed) {
			return nil
		}
		return httpErr
	case bridgeErr := <-bridgeErrChan:
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if errors.Is(bridgeErr, context.Canceled) {
			return nil
		}
		return bridgeErr
	case tunnelErr := <-tunnelErrChan:
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if errors.Is(tunnelErr, context.Canceled) {
			return nil
		}
		return tunnelErr
	case trafficErr := <-trafficErrChan:
		if ipcServer != nil {
			_ = ipcServer.Close()
		}
		if httpServer != nil {
			_ = httpServer.Close()
		}
		if errors.Is(trafficErr, context.Canceled) {
			return nil
		}
		return trafficErr
	}
}

func (r *Runtime) resolveRuntimeServers(options RunOptions) (*localRPCServer, *httpAgentServer, error) {
	if r == nil {
		return nil, nil, errors.New("runtime is nil")
	}
	if err := options.Validate(r.cfg); err != nil {
		return nil, nil, err
	}
	var (
		ipcServer  *localRPCServer
		httpServer *httpAgentServer
		err        error
	)
	if options.EnableLocalRPC {
		ipcServer, err = newLocalRPCServer(r)
		if err != nil {
			return nil, nil, err
		}
	}
	if options.EnableWeb {
		httpServer, err = newHTTPServer(r)
		if err != nil {
			if ipcServer != nil {
				_ = ipcServer.Close()
			}
			return nil, nil, err
		}
	}
	return ipcServer, httpServer, nil
}

// Shutdown allows graceful teardown.
func (r *Runtime) Shutdown(ctx context.Context) error {
	_ = ctx
	if r == nil {
		return nil
	}
	r.shutdownMu.Lock()
	defer r.shutdownMu.Unlock()
	if r.stopped {
		return nil
	}
	r.stopped = true
	close(r.shutdownCh)
	if r.ipcServer != nil {
		_ = r.ipcServer.Close()
	}
	if r.httpServer != nil {
		_ = r.httpServer.Close()
	}
	r.closeCurrentControlChannel()
	return nil
}
