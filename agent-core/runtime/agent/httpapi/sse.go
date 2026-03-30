package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/hostapi"
)

const (
	sseProtocolVersion   = "v1"
	sseDefaultInterval   = 5 * time.Second
	sseHeartbeatInterval = 15 * time.Second

	sseEventReady     = "agent.ready"
	sseEventSnapshot  = "agent.snapshot"
	sseEventHeartbeat = "agent.heartbeat"
)

type sseEnvelope struct {
	Version      string         `json:"version"`
	Type         string         `json:"type"`
	ServerTimeMS uint64         `json:"server_time_ms"`
	Sequence     uint64         `json:"sequence,omitempty"`
	IntervalMS   uint64         `json:"interval_ms,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
}

func (server *Server) handleEventsStream(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		server.writeJSONError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	if _, ok := server.requireSession(writer, request); !ok {
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		server.writeJSONError(writer, http.StatusInternalServerError, "UNSUPPORTED_STREAM", "response writer does not support streaming")
		return
	}

	snapshotPayload, failure, err := server.buildConsoleSnapshot(request.Context())
	if err != nil {
		server.writeJSONError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	if failure != nil {
		statusCode := http.StatusInternalServerError
		if failure.Code == "INVALID_REQUEST" {
			statusCode = http.StatusBadRequest
		}
		if failure.Code == "METHOD_NOT_ALLOWED" {
			statusCode = http.StatusMethodNotAllowed
		}
		server.writeJSONError(writer, statusCode, failure.Code, failure.Message)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(writer, "retry: %d\n\n", sseDefaultInterval.Milliseconds())
	flusher.Flush()

	sequence := uint64(1)
	readyEnvelope := sseEnvelope{
		Version:      sseProtocolVersion,
		Type:         "ready",
		ServerTimeMS: uint64(server.nowUTC().UnixMilli()),
		IntervalMS:   uint64(sseDefaultInterval.Milliseconds()),
	}
	if err := writeSSEJSONEvent(writer, flusher, sseEventReady, formatSSEEventID(sequence), readyEnvelope); err != nil {
		return
	}
	sequence++
	if err := server.writeSnapshotEvent(writer, flusher, snapshotPayload, &sequence); err != nil {
		return
	}

	snapshotTicker := time.NewTicker(sseDefaultInterval)
	heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
	defer snapshotTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-request.Context().Done():
			return
		case <-snapshotTicker.C:
			nextSnapshotPayload, nextFailure, nextErr := server.buildConsoleSnapshot(request.Context())
			if nextErr != nil || nextFailure != nil {
				return
			}
			if err := server.writeSnapshotEvent(writer, flusher, nextSnapshotPayload, &sequence); err != nil {
				return
			}
		case <-heartbeatTicker.C:
			heartbeatEnvelope := sseEnvelope{
				Version:      sseProtocolVersion,
				Type:         "heartbeat",
				ServerTimeMS: uint64(server.nowUTC().UnixMilli()),
				Sequence:     sequence,
			}
			if err := writeSSEJSONEvent(writer, flusher, sseEventHeartbeat, formatSSEEventID(sequence), heartbeatEnvelope); err != nil {
				return
			}
			sequence++
		}
	}
}

func (server *Server) writeSnapshotEvent(
	writer http.ResponseWriter,
	flusher http.Flusher,
	payload map[string]any,
	sequence *uint64,
) error {
	if sequence == nil {
		return fmt.Errorf("write snapshot event: nil sequence")
	}
	eventEnvelope := sseEnvelope{
		Version:      sseProtocolVersion,
		Type:         "snapshot",
		ServerTimeMS: uint64(server.nowUTC().UnixMilli()),
		Sequence:     *sequence,
		Payload:      payload,
	}
	if err := writeSSEJSONEvent(writer, flusher, sseEventSnapshot, formatSSEEventID(*sequence), eventEnvelope); err != nil {
		return err
	}
	*sequence = *sequence + 1
	return nil
}

func (server *Server) buildConsoleSnapshot(ctx context.Context) (map[string]any, *hostapi.Failure, error) {
	snapshotMethods := []struct {
		key    string
		method hostapi.Method
	}{
		{key: "agent", method: hostapi.MethodAgentSnapshot},
		{key: "session", method: hostapi.MethodSessionSnapshot},
		{key: "services", method: hostapi.MethodServiceList},
		{key: "tunnels", method: hostapi.MethodTunnelList},
		{key: "traffic", method: hostapi.MethodTrafficStatsSnapshot},
		{key: "diagnose", method: hostapi.MethodDiagnoseSnapshot},
		{key: "logs", method: hostapi.MethodDiagnoseLogs},
		{key: "config", method: hostapi.MethodConfigSnapshot},
	}

	payload := make(map[string]any, len(snapshotMethods))
	for _, item := range snapshotMethods {
		response, failure := server.handler.Handle(ctx, hostapi.Request{
			Method:  item.method,
			Payload: json.RawMessage(`{}`),
		})
		if failure != nil {
			return nil, failure, nil
		}
		payload[item.key] = response.Payload
	}
	return payload, nil, nil
}

func writeSSEJSONEvent(
	writer http.ResponseWriter,
	flusher http.Flusher,
	eventName string,
	eventID string,
	payload any,
) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("write sse json event: marshal payload: %w", err)
	}
	if _, err := fmt.Fprintf(writer, "id: %s\n", strings.TrimSpace(eventID)); err != nil {
		return fmt.Errorf("write sse json event: write id: %w", err)
	}
	if _, err := fmt.Fprintf(writer, "event: %s\n", strings.TrimSpace(eventName)); err != nil {
		return fmt.Errorf("write sse json event: write event: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if _, err := fmt.Fprintf(writer, "data: %s\n", line); err != nil {
			return fmt.Errorf("write sse json event: write data: %w", err)
		}
	}
	if _, err := fmt.Fprint(writer, "\n"); err != nil {
		return fmt.Errorf("write sse json event: write separator: %w", err)
	}
	flusher.Flush()
	return nil
}

func formatSSEEventID(sequence uint64) string {
	return strconv.FormatUint(sequence, 10)
}
