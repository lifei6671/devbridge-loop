package app

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	localRPCHeaderLen  = 32
	localRPCMaxBodyLen = 1_048_576
)

var localRPCMagic = [4]byte{'L', 'R', 'P', 'C'}

const localRPCVersion uint16 = 1

type localRPCFrameType uint16

const (
	localRPCFrameTypeRequest  localRPCFrameType = 1
	localRPCFrameTypeResponse localRPCFrameType = 2
	localRPCFrameTypeEvent    localRPCFrameType = 3
	localRPCFrameTypePing     localRPCFrameType = 4
	localRPCFrameTypePong     localRPCFrameType = 5
)

type localRPCFrame struct {
	frameType localRPCFrameType
	flags     uint32
	requestID [16]byte
	body      []byte
}

func isZeroRequestID(requestID [16]byte) bool {
	for _, value := range requestID {
		if value != 0 {
			return false
		}
	}
	return true
}

func validateLocalRPCFrame(frame localRPCFrame) error {
	if len(frame.body) > localRPCMaxBodyLen {
		return errorsWithCode("FRAME_TOO_LARGE", "BodyLen exceeds 1MiB")
	}
	switch frame.frameType {
	case localRPCFrameTypeRequest, localRPCFrameTypeResponse:
		if isZeroRequestID(frame.requestID) {
			return errorsWithCode("PROTOCOL_ERROR", "request/response request_id cannot be zero")
		}
	case localRPCFrameTypeEvent, localRPCFrameTypePing, localRPCFrameTypePong:
		if !isZeroRequestID(frame.requestID) {
			return errorsWithCode("PROTOCOL_ERROR", "event/ping/pong request_id must be zero")
		}
	default:
		return errorsWithCode("PROTOCOL_ERROR", fmt.Sprintf("unknown frame type=%d", frame.frameType))
	}
	if (frame.frameType == localRPCFrameTypePing || frame.frameType == localRPCFrameTypePong) && len(frame.body) > 0 {
		return errorsWithCode("PROTOCOL_ERROR", "ping/pong body must be empty")
	}
	if len(frame.body) > 0 {
		var parsedBody map[string]any
		if err := json.Unmarshal(frame.body, &parsedBody); err != nil {
			return errorsWithCode("PROTOCOL_ERROR", fmt.Sprintf("json decode failed: %v", err))
		}
		if _, found := parsedBody["request_id"]; found {
			return errorsWithCode("PROTOCOL_ERROR", "json body must not contain request_id")
		}
	}
	return nil
}

func readLocalRPCFrame(reader io.Reader) (localRPCFrame, error) {
	header := make([]byte, localRPCHeaderLen)
	if _, err := io.ReadFull(reader, header); err != nil {
		return localRPCFrame{}, err
	}
	if string(header[:4]) != string(localRPCMagic[:]) {
		return localRPCFrame{}, errorsWithCode("PROTOCOL_ERROR", "invalid frame magic")
	}
	version := binary.BigEndian.Uint16(header[4:6])
	if version != localRPCVersion {
		return localRPCFrame{}, errorsWithCode("PROTOCOL_ERROR", fmt.Sprintf("unsupported protocol version=%d", version))
	}
	frame := localRPCFrame{
		frameType: localRPCFrameType(binary.BigEndian.Uint16(header[6:8])),
		flags:     binary.BigEndian.Uint32(header[8:12]),
	}
	copy(frame.requestID[:], header[12:28])
	bodyLen := binary.BigEndian.Uint32(header[28:32])
	if bodyLen > localRPCMaxBodyLen {
		return localRPCFrame{}, errorsWithCode("FRAME_TOO_LARGE", "BodyLen exceeds 1MiB")
	}
	if bodyLen > 0 {
		frame.body = make([]byte, bodyLen)
		if _, err := io.ReadFull(reader, frame.body); err != nil {
			return localRPCFrame{}, err
		}
	}
	if err := validateLocalRPCFrame(frame); err != nil {
		return localRPCFrame{}, err
	}
	return frame, nil
}

func writeLocalRPCFrame(writer io.Writer, frame localRPCFrame) error {
	if err := validateLocalRPCFrame(frame); err != nil {
		return err
	}
	header := make([]byte, localRPCHeaderLen)
	copy(header[:4], localRPCMagic[:])
	binary.BigEndian.PutUint16(header[4:6], localRPCVersion)
	binary.BigEndian.PutUint16(header[6:8], uint16(frame.frameType))
	binary.BigEndian.PutUint32(header[8:12], frame.flags)
	copy(header[12:28], frame.requestID[:])
	binary.BigEndian.PutUint32(header[28:32], uint32(len(frame.body)))
	if _, err := writer.Write(header); err != nil {
		return err
	}
	if len(frame.body) > 0 {
		if _, err := writer.Write(frame.body); err != nil {
			return err
		}
	}
	if flusher, ok := writer.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return err
		}
	}
	return nil
}
