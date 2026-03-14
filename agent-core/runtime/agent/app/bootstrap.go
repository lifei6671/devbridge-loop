package app

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/service"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/traffic"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
)

// BootstrapOptions 定义 runtime 初始化时的可选覆盖项。
type BootstrapOptions struct {
	// TunnelPoolOverride 允许外部按字段覆盖 tunnelPool 参数。
	// 首版仅在 Bootstrap 时生效，不支持运行时热更新。
	TunnelPoolOverride *TunnelPoolOverride
}

// Runtime wires the agent runtime subsystems together.
type Runtime struct {
	cfg Config

	startedAt time.Time

	bridgeMu          sync.RWMutex
	bridgeDesiredUp   bool
	bridgeState       string
	bridgeSession     string
	bridgeEpoch       uint64
	reconnects        uint64
	heartbeatAt       time.Time
	heartbeatSentAt   time.Time
	updatedAt         time.Time
	lastErr           string
	retryFailStreak   uint32
	retryBackoff      time.Duration
	nextRetryAt       time.Time
	tunnelIDSequence  uint64
	bridgeCommandChan chan bridgeCommand

	controlChannel   transport.ControlChannel
	tcpTransport     *tcpbinding.Transport
	grpcTransport    *grpcbinding.Transport
	grpcClient       transportgen.GRPCH2TransportServiceClient
	grpcConn         *grpc.ClientConn
	tunnelRegistry   *tunnel.Registry
	tunnelManager    *tunnel.Manager
	refillHandler    *control.RefillHandler
	serviceCatalog   *service.Catalog
	controlPublisher *control.Publisher
	tunnelReporter   *control.TunnelReporter
	healthReporter   *control.HealthReporter
	trafficAcceptor  *traffic.Acceptor
	trafficOpener    *traffic.Opener

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

	ipcServer  *localRPCServer
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
		bridgeState:        "CONNECTING",
		updatedAt:          time.Now().UTC(),
		bridgeCommandChan:  make(chan bridgeCommand, 8),
		serviceCatalog:     serviceCatalog,
		controlPublisher:   control.NewPublisher("", 0, 0),
		healthReporter:     control.NewHealthReporter(control.HealthReporterOptions{}),
		tunnelAssociations: make(map[string]tunnelAssociation),
		diagnoseEvents:     make([]runtimeDiagnoseEvent, 0, runtimeDiagnoseEventBufferSize),
		metrics:            obs.NewMetrics(),
		shutdownCh:         make(chan struct{}),
	}, nil
}

// Run starts the runtime and serves local IPC for Tauri host.
func (r *Runtime) Run(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
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
	ipcServer, err := newLocalRPCServer(r)
	if err != nil {
		return err
	}
	r.shutdownMu.Lock()
	r.ipcServer = ipcServer
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

	serverErrChan := make(chan error, 1)
	go func() {
		serverErrChan <- ipcServer.Serve(runContext)
	}()

	select {
	case <-runContext.Done():
		_ = ipcServer.Close()
		return nil
	case serverErr := <-serverErrChan:
		_ = ipcServer.Close()
		if errors.Is(serverErr, context.Canceled) {
			return nil
		}
		return serverErr
	case bridgeErr := <-bridgeErrChan:
		_ = ipcServer.Close()
		if errors.Is(bridgeErr, context.Canceled) {
			return nil
		}
		return bridgeErr
	case tunnelErr := <-tunnelErrChan:
		_ = ipcServer.Close()
		if errors.Is(tunnelErr, context.Canceled) {
			return nil
		}
		return tunnelErr
	case trafficErr := <-trafficErrChan:
		_ = ipcServer.Close()
		if errors.Is(trafficErr, context.Canceled) {
			return nil
		}
		return trafficErr
	}
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
	r.closeCurrentControlChannel()
	return nil
}
