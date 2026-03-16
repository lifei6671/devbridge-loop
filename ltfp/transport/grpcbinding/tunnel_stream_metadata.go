package grpcbinding

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
)

const (
	// TunnelStreamMetadataTunnelIDKey 标识 gRPC TunnelStream 上的 tunnel_id 元数据键。
	TunnelStreamMetadataTunnelIDKey = "x-devbridge-tunnel-id"
	// TunnelStreamMetadataSessionIDKey 标识 gRPC TunnelStream 上的 session_id 元数据键。
	TunnelStreamMetadataSessionIDKey = "x-devbridge-session-id"
	// TunnelStreamMetadataSessionEpochKey 标识 gRPC TunnelStream 上的 session_epoch 元数据键。
	TunnelStreamMetadataSessionEpochKey = "x-devbridge-session-epoch"

	// TunnelMetaLabelTunnelIDSource 标识 transport.TunnelMeta.Labels 中 tunnel_id 来源键。
	TunnelMetaLabelTunnelIDSource = "devbridge.tunnel_id_source"
	// TunnelIDSourceStreamMetadata 表示 tunnel_id 来源于 gRPC stream metadata。
	TunnelIDSourceStreamMetadata = "stream_metadata"
)

// WithTunnelStreamMetadata 返回附带 tunnel 关联元数据的 outgoing context。
func WithTunnelStreamMetadata(ctx context.Context, tunnelID string, sessionID string, sessionEpoch uint64) context.Context {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return normalizedContext
	}
	pairs := []string{
		TunnelStreamMetadataTunnelIDKey, normalizedTunnelID,
	}
	if normalizedSessionID := strings.TrimSpace(sessionID); normalizedSessionID != "" {
		pairs = append(pairs, TunnelStreamMetadataSessionIDKey, normalizedSessionID)
	}
	if sessionEpoch > 0 {
		pairs = append(pairs, TunnelStreamMetadataSessionEpochKey, strconv.FormatUint(sessionEpoch, 10))
	}
	return metadata.AppendToOutgoingContext(normalizedContext, pairs...)
}

// IncomingTunnelStreamTunnelID 从 incoming context 中提取 tunnel_id。
func IncomingTunnelStreamTunnelID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, value := range md.Get(TunnelStreamMetadataTunnelIDKey) {
		if normalizedTunnelID := strings.TrimSpace(value); normalizedTunnelID != "" {
			return normalizedTunnelID
		}
	}
	return ""
}
