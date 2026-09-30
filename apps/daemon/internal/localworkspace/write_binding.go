package localworkspace

import "sync"

type fileWriter struct {
	mu        sync.Mutex
	uncertain bool
}
