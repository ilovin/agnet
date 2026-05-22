package hermesclient

import (
	"bufio"
	"io"
	"strings"
)

type SSEEvent struct {
	Data string
}

// decodeSSE reads SSE stream and emits data events
func decodeSSE(r io.Reader) (<-chan SSEEvent, <-chan error) {
	events := make(chan SSEEvent, 10)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				events <- SSEEvent{Data: data}
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- err
		}
	}()
	return events, errs
}
