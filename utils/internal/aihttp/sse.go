package aihttp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Event preserves a native SSE event without interpreting its JSON payload.
type Event struct {
	Type, ID string
	Data     json.RawMessage
}

// ReadSSE parses blank-line-delimited events with a bounded event size.
func ReadSSE(r io.Reader, limit int64, handle func(Event) error) error {
	if handle == nil {
		return fmt.Errorf("aihttp: nil stream callback")
	}
	br := bufio.NewReader(r)
	var ev Event
	var data []string
	var size int64
	for {
		var line []byte
		for {
			part, e := br.ReadSlice('\n')
			size += int64(len(part))
			if size > limit {
				return fmt.Errorf("aihttp: SSE event exceeds %d bytes", limit)
			}
			line = append(line, part...)
			if e == bufio.ErrBufferFull {
				continue
			}
			if e != nil {
				if e == io.EOF {
					if len(line) > 0 || len(data) > 0 {
						return io.ErrUnexpectedEOF
					}
					return nil
				}
				return e
			}
			break
		}
		s := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if s == "" {
			if len(data) > 0 {
				ev.Data = json.RawMessage(strings.Join(data, "\n"))
				if err := handle(ev); err != nil {
					return err
				}
			}
			ev = Event{}
			data = nil
			size = 0
			continue
		}
		if strings.HasPrefix(s, ":") {
			continue
		}
		k, v, _ := strings.Cut(s, ":")
		v = strings.TrimPrefix(v, " ")
		switch k {
		case "event":
			ev.Type = v
		case "id":
			if !strings.ContainsRune(v, 0) {
				ev.ID = v
			}
		case "data":
			data = append(data, v)
		}
	}
}
