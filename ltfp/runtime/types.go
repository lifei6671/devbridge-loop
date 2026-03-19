package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TrafficMeta 描述一次数据面流量的核心元数据。
//
// 约束：
// 1. 仅允许使用 LogicalServiceID，不允许重新引入旧服务主键语义。
// 2. 字段面向 runtime/protocol 层，不暴露 transport 内部实现细节。
type TrafficMeta struct {
	TrafficID        string
	SessionID        string
	SessionEpoch     uint64
	LogicalServiceID string
	Labels           map[string]string
	DeadlineAt       *time.Time
}

// Validate 校验 TrafficMeta 的最小语义约束。
func (meta TrafficMeta) Validate() error {
	if strings.TrimSpace(meta.TrafficID) == "" {
		// traffic_id 为空时无法建立稳定的流量追踪键。
		return fmt.Errorf("validate traffic meta: %w: empty traffic_id", transport.ErrInvalidArgument)
	}
	if strings.TrimSpace(meta.LogicalServiceID) == "" {
		// runtime 层必须依赖 logical_service_id 做路由和审计。
		return fmt.Errorf("validate traffic meta: %w: empty logical_service_id", transport.ErrInvalidArgument)
	}
	return nil
}

// TrafficState 描述 runtime 层的流量状态机阶段。
type TrafficState string

const (
	// TrafficStateReserved 表示已分配 tunnel，待发送 Open。
	TrafficStateReserved TrafficState = "reserved"
	// TrafficStateOpenSent 表示 Open 已发送，待接收 OpenAck。
	TrafficStateOpenSent TrafficState = "open_sent"
	// TrafficStateEstablished 表示收到成功 OpenAck，进入数据转发。
	TrafficStateEstablished TrafficState = "established"
	// TrafficStateClosing 表示流量进入协议性关闭阶段。
	TrafficStateClosing TrafficState = "closing"
	// TrafficStateClosed 表示流量已正常关闭。
	TrafficStateClosed TrafficState = "closed"
	// TrafficStateReset 表示流量被异常重置。
	TrafficStateReset TrafficState = "reset"
	// TrafficStateRejected 表示 Open 被拒绝。
	TrafficStateRejected TrafficState = "rejected"
)

// TrafficFrameType 描述 runtime 层的帧类型。
type TrafficFrameType string

const (
	// TrafficFrameOpen 表示 Open 帧。
	TrafficFrameOpen TrafficFrameType = "open"
	// TrafficFrameOpenAck 表示 OpenAck 帧。
	TrafficFrameOpenAck TrafficFrameType = "open_ack"
	// TrafficFrameData 表示 Data 帧。
	TrafficFrameData TrafficFrameType = "data"
	// TrafficFrameClose 表示 Close 帧。
	TrafficFrameClose TrafficFrameType = "close"
	// TrafficFrameCloseAck 表示 CloseAck 帧。
	TrafficFrameCloseAck TrafficFrameType = "close_ack"
	// TrafficFrameReset 表示 Reset 帧。
	TrafficFrameReset TrafficFrameType = "reset"
	// TrafficFrameRecycle 表示 TunnelRecycle 帧。
	TrafficFrameRecycle TrafficFrameType = "recycle"
	// TrafficFrameRecycleAck 表示 TunnelRecycleAck 帧。
	TrafficFrameRecycleAck TrafficFrameType = "recycle_ack"
)

// TrafficOpenAck 描述 Open 的应答语义。
type TrafficOpenAck struct {
	Accepted bool
	Code     string
	Message  string
}

// TrafficClose 描述协议性关闭语义。
type TrafficClose struct {
	Code    string
	Message string
}

// TrafficCloseAck 描述协议性关闭确认语义。
type TrafficCloseAck struct {
	Accepted bool
	Code     string
	Message  string
}

// TrafficReset 描述异常终止语义。
type TrafficReset struct {
	Code    string
	Message string
}

// TunnelRecycle 描述服务端发起的 tunnel 回收请求。
type TunnelRecycle struct {
	TunnelID              string
	RecycleSeq            uint64
	IsFinal               bool
	CompletedTrafficCount int32
}

// TunnelRecycleAck 描述 Agent 对 tunnel 回收请求的确认。
type TunnelRecycleAck struct {
	TunnelID   string
	RecycleSeq uint64
	Accepted   bool
	ErrorCode  string
	Message    string
}

// TrafficFrame 描述 runtime 层统一帧对象。
//
// 每一帧必须且只能携带一种语义载荷，对应 Type 字段。
type TrafficFrame struct {
	Type       TrafficFrameType
	Open       *TrafficMeta
	OpenAck    *TrafficOpenAck
	Data       []byte
	Close      *TrafficClose
	CloseAck   *TrafficCloseAck
	Reset      *TrafficReset
	Recycle    *TunnelRecycle
	RecycleAck *TunnelRecycleAck
}

// Validate 校验帧类型与载荷的一致性。
func (frame TrafficFrame) Validate() error {
	switch frame.Type {
	case TrafficFrameOpen:
		if frame.Open == nil {
			return fmt.Errorf("validate traffic frame: %w: open payload is nil", transport.ErrInvalidArgument)
		}
		if err := frame.Open.Validate(); err != nil {
			return err
		}
		if frame.OpenAck != nil || len(frame.Data) > 0 || frame.Close != nil || frame.CloseAck != nil || frame.Reset != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			// Open 帧禁止混入其他载荷，避免对端解码歧义。
			return fmt.Errorf("validate traffic frame: %w: open frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameOpenAck:
		if frame.OpenAck == nil {
			return fmt.Errorf("validate traffic frame: %w: open_ack payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || len(frame.Data) > 0 || frame.Close != nil || frame.CloseAck != nil || frame.Reset != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: open_ack frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameData:
		if frame.Open != nil || frame.OpenAck != nil || frame.Close != nil || frame.CloseAck != nil || frame.Reset != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: data frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameClose:
		if frame.Close == nil {
			return fmt.Errorf("validate traffic frame: %w: close payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || frame.OpenAck != nil || len(frame.Data) > 0 || frame.CloseAck != nil || frame.Reset != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: close frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameCloseAck:
		if frame.CloseAck == nil {
			return fmt.Errorf("validate traffic frame: %w: close_ack payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || frame.OpenAck != nil || len(frame.Data) > 0 || frame.Close != nil || frame.Reset != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: close_ack frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameReset:
		if frame.Reset == nil {
			return fmt.Errorf("validate traffic frame: %w: reset payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || frame.OpenAck != nil || len(frame.Data) > 0 || frame.Close != nil || frame.CloseAck != nil || frame.Recycle != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: reset frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameRecycle:
		if frame.Recycle == nil {
			return fmt.Errorf("validate traffic frame: %w: recycle payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || frame.OpenAck != nil || len(frame.Data) > 0 || frame.Close != nil || frame.CloseAck != nil || frame.Reset != nil || frame.RecycleAck != nil {
			return fmt.Errorf("validate traffic frame: %w: recycle frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	case TrafficFrameRecycleAck:
		if frame.RecycleAck == nil {
			return fmt.Errorf("validate traffic frame: %w: recycle_ack payload is nil", transport.ErrInvalidArgument)
		}
		if frame.Open != nil || frame.OpenAck != nil || len(frame.Data) > 0 || frame.Close != nil || frame.CloseAck != nil || frame.Reset != nil || frame.Recycle != nil {
			return fmt.Errorf("validate traffic frame: %w: recycle_ack frame carries unexpected payload", transport.ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("validate traffic frame: %w: unknown frame type=%q", transport.ErrInvalidArgument, frame.Type)
	}
	return nil
}

// TrafficProtocol 定义 runtime 层统一帧读写入口。
//
// 该接口故意不引入 context.Context；
// 超时与取消统一由 transport.Tunnel 的 deadline 机制控制。
type TrafficProtocol interface {
	WriteFrame(tunnel transport.Tunnel, frame TrafficFrame) error
	ReadFrame(tunnel transport.Tunnel) (TrafficFrame, error)
}
