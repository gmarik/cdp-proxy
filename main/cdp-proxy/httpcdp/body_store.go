package httpcdp

import (
	"bytes"
	"sync"
)

type bodyStore struct {
	m sync.Map
}

func newStore() *bodyStore {
	return &bodyStore{}
}

func (bs *bodyStore) Load(key string) (*bytes.Buffer, bool) {
	val, ok := bs.m.Load(key)
	if !ok {
		return nil, false
	}
	if buf, ok := val.(*bytes.Buffer); ok {
		return buf, true
	}
	return nil, false
}

func (bs *bodyStore) LoadOrStore(key string, buf *bytes.Buffer) (actual *bytes.Buffer, loaded bool) {
	val, ok := bs.m.LoadOrStore(key, buf)
	return val.(*bytes.Buffer), ok
}
