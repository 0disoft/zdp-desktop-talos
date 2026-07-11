package workeripc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const WorkerVersion = "0.1.22"

func Serve(reader io.Reader, writer io.Writer) error {
	decoder := NewDecoder(reader, MaxFrameSize)
	encoder := NewEncoder(writer, MaxFrameSize)
	for {
		message, err := decoder.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if message.Version != ProtocolVersion {
			_ = encoder.Write(Message{
				Version:   ProtocolVersion,
				Type:      "error",
				RequestID: message.RequestID,
				Error: &ErrorPayload{
					Code:      "WORKER_PROTOCOL_VERSION_MISMATCH",
					Message:   "worker and client protocol versions differ",
					Retryable: false,
				},
			})
			return ErrVersionMismatch
		}

		switch message.Type {
		case "handshake":
			payload, err := json.Marshal(HandshakePayload{
				ProtocolVersion: ProtocolVersion,
				WorkerVersion:   WorkerVersion,
				Capabilities:    []string{"heartbeat", "shutdown"},
			})
			if err != nil {
				return err
			}
			if err := encoder.Write(Message{Version: ProtocolVersion, Type: "handshake_result", RequestID: message.RequestID, Payload: payload}); err != nil {
				return err
			}
		case "heartbeat":
			if err := encoder.Write(Message{Version: ProtocolVersion, Type: "heartbeat_result", RequestID: message.RequestID, Payload: json.RawMessage(`{"status":"ok"}`)}); err != nil {
				return err
			}
		case "shutdown":
			if err := encoder.Write(Message{Version: ProtocolVersion, Type: "shutdown_result", RequestID: message.RequestID, Payload: json.RawMessage(`{"status":"stopped"}`)}); err != nil {
				return err
			}
			return nil
		default:
			if err := encoder.Write(Message{
				Version:   ProtocolVersion,
				Type:      "error",
				RequestID: message.RequestID,
				Error:     &ErrorPayload{Code: "WORKER_MESSAGE_UNSUPPORTED", Message: fmt.Sprintf("unsupported message type %q", message.Type), Retryable: false},
			}); err != nil {
				return err
			}
		}
	}
}
