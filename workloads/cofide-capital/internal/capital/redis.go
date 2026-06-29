package capital

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

const EventChannel = "cofide-capital:events"

func PublishEvent(ctx context.Context, service, eventType string, details map[string]any) {
	event := Event{
		Timestamp: time.Now().UTC(),
		Service:   service,
		EventType: eventType,
		Details:   details,
	}
	if err := publish(ctx, EventChannel, event); err != nil {
		slog.Warn("failed to publish event", "service", service, "event_type", eventType, "error", err)
	}
}

func publish(ctx context.Context, channel string, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	conn, err := dialRedis(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if _, err = fmt.Fprintf(conn, "*3\r\n$7\r\nPUBLISH\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(channel), channel, len(payload), payload); err != nil {
		return err
	}
	_, err = readRESP(bufio.NewReader(conn))
	return err
}

func SubscribeEvents(ctx context.Context) (<-chan Event, error) {
	conn, err := dialRedis(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = fmt.Fprintf(conn, "*2\r\n$9\r\nSUBSCRIBE\r\n$%d\r\n%s\r\n", len(EventChannel), EventChannel); err != nil {
		_ = conn.Close()
		return nil, err
	}

	events := make(chan Event, 16)
	go func() {
		defer close(events)
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		_, _ = readRESP(reader) // subscription acknowledgement
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			value, err := readRESP(reader)
			if err != nil {
				slog.Warn("redis subscription ended", "error", err)
				return
			}
			items, ok := value.([]any)
			if !ok || len(items) < 3 {
				continue
			}
			payload, ok := items[2].(string)
			if !ok {
				continue
			}
			var event Event
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func dialRedis(ctx context.Context) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return dialer.DialContext(ctx, "tcp", Env("REDIS_ADDR", "redis:6379"))
}

func readRESP(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch prefix {
	case '+':
		return readLine(r)
	case ':':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		return strconv.Atoi(line)
	case '$':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return "", nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		items := make([]any, 0, n)
		for range n {
			item, err := readRESP(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	case '-':
		line, _ := readLine(r)
		return nil, fmt.Errorf("redis error: %s", line)
	default:
		return nil, fmt.Errorf("unexpected RESP prefix %q", prefix)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
