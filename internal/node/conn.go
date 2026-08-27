package node

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// rpcTimeout bounds how long the server waits for a node to answer a
// command. Storage nodes sit behind consumer Wi-Fi, so it is generous.
const rpcTimeout = 15 * time.Second

// Conn is an established WebSocket connection to a provisioned node.
type Conn struct {
	ID     string
	Name   string
	PubKey []byte

	ws      writeFlusher
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[uint64]chan map[string]interface{}
	nextID  uint64

	closedCh chan struct{}
}

type writeFlusher interface {
	WriteJSON(v interface{}) error
	Close() error
}

// Send writes a JSON message to the node. Safe for concurrent use.
func (c *Conn) Send(msg map[string]interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(msg)
}

// Call performs a synchronous command/response round trip: it assigns a
// req_id, sends the message and waits for the matching "resp" from the node.
func (c *Conn) Call(action string, params map[string]interface{}) (map[string]interface{}, error) {
	c.mu.Lock()
	c.nextID++
	reqID := c.nextID
	ch := make(chan map[string]interface{}, 1)
	c.pending[reqID] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
	}()

	msg := map[string]interface{}{
		"type":   "cmd",
		"req_id": reqID,
		"action": action,
	}
	for k, v := range params {
		msg[k] = v
	}
	if err := c.Send(msg); err != nil {
		return nil, fmt.Errorf("send %s: %w", action, err)
	}

	select {
	case resp := <-ch:
		if ok, _ := resp["ok"].(bool); !ok {
			errMsg, _ := resp["error"].(string)
			return resp, fmt.Errorf("node error: %s", errMsg)
		}
		return resp, nil
	case <-time.After(rpcTimeout):
		return nil, fmt.Errorf("timeout waiting for %s response", action)
	case <-c.closedCh:
		return nil, fmt.Errorf("node disconnected during %s", action)
	}
}

// dispatch routes an incoming message with a req_id back to its caller.
func (c *Conn) dispatch(reqID float64, msg map[string]interface{}) {
	id := uint64(reqID)
	c.mu.Lock()
	ch := c.pending[id]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// readJSON decodes one text frame into a generic object.
func decodeMessage(payload []byte) (map[string]interface{}, error) {
	var msg map[string]interface{}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}
