package quicbinding

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	quic "github.com/quic-go/quic-go"
)

const defaultTunnelHandshakeMaxPayloadSize = 64 * 1024

func writeTunnelHandshake(stream *quic.Stream, meta transport.TunnelMeta, dialLocalAddr string) error {
	if stream == nil {
		return fmt.Errorf("write quic tunnel handshake: %w: nil stream", transport.ErrInvalidArgument)
	}
	handshakePayload, err := json.Marshal(pb.TunnelDialAnnounce{
		SessionID:     strings.TrimSpace(meta.SessionID),
		SessionEpoch:  meta.SessionEpoch,
		TunnelID:      strings.TrimSpace(meta.TunnelID),
		DialLocalAddr: strings.TrimSpace(dialLocalAddr),
		TimestampUnix: meta.CreatedAt.Unix(),
	})
	if err != nil {
		return fmt.Errorf("write quic tunnel handshake: marshal payload: %w", err)
	}
	if len(handshakePayload) > defaultTunnelHandshakeMaxPayloadSize {
		return fmt.Errorf(
			"write quic tunnel handshake: %w: payload_size=%d",
			transport.ErrInvalidArgument,
			len(handshakePayload),
		)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(handshakePayload)))
	if err := writeAllToStream(stream, header); err != nil {
		return fmt.Errorf("write quic tunnel handshake: write header: %w", err)
	}
	if err := writeAllToStream(stream, handshakePayload); err != nil {
		return fmt.Errorf("write quic tunnel handshake: write payload: %w", err)
	}
	return nil
}

func readTunnelHandshake(stream *quic.Stream, maxPayloadBytes int) (pb.TunnelDialAnnounce, error) {
	if stream == nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read quic tunnel handshake: %w: nil stream", transport.ErrInvalidArgument)
	}
	maxAllowedPayload := maxPayloadBytes
	if maxAllowedPayload <= 0 {
		maxAllowedPayload = defaultTunnelHandshakeMaxPayloadSize
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(stream, header); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read quic tunnel handshake: read header: %w", err)
	}
	payloadSize := int(binary.BigEndian.Uint32(header))
	if payloadSize <= 0 {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read quic tunnel handshake: %w: empty payload", transport.ErrInvalidArgument)
	}
	if payloadSize > maxAllowedPayload {
		return pb.TunnelDialAnnounce{}, fmt.Errorf(
			"read quic tunnel handshake: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			payloadSize,
			maxAllowedPayload,
		)
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(stream, payload); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read quic tunnel handshake: read payload: %w", err)
	}
	var handshake pb.TunnelDialAnnounce
	if err := json.Unmarshal(payload, &handshake); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read quic tunnel handshake: decode payload: %w", err)
	}
	return handshake, nil
}

func tunnelMetaFromHandshake(handshake pb.TunnelDialAnnounce) transport.TunnelMeta {
	createdAt := time.Now().UTC()
	if handshake.TimestampUnix > 0 {
		createdAt = time.Unix(handshake.TimestampUnix, 0).UTC()
	}
	return transport.TunnelMeta{
		TunnelID:     strings.TrimSpace(handshake.TunnelID),
		SessionID:    strings.TrimSpace(handshake.SessionID),
		SessionEpoch: handshake.SessionEpoch,
		CreatedAt:    createdAt,
	}
}
