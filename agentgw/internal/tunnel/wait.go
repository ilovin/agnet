package tunnel

import "time"

func waitMs(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
