package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Envelope is one JSON object per line (NDJSON) on control and connect handshakes.
type Envelope struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	Token    string `json:"token,omitempty"`
	OK       bool   `json:"ok,omitempty"`
	Message  string `json:"message,omitempty"`
	Network  string `json:"network,omitempty"`
	Address  string `json:"address,omitempty"`
	Ts       int64  `json:"ts,omitempty"`
}

const (
	TypeRegister      = "register"
	TypeRegisterAck   = "register_ack"
	TypeHeartbeat     = "heartbeat"
	TypeHeartbeatAck  = "heartbeat_ack"
	TypeConnect       = "connect"
	TypeConnectResult = "connect_result"
)

func WriteLine(w io.Writer, v *Envelope) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func ReadLine(r *bufio.Reader) (*Envelope, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	// trim newline
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, fmt.Errorf("protocol: invalid json line: %w", err)
	}
	return &env, nil
}
