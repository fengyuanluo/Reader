package discovery

import "sync"

// singleflight 是进程内按 key 去重的并发单飞：同 key 的并发调用共享同一次结果
// （对齐 Legado per-source Mutex），避免 JS 分类脚本被重复执行（S14-10）。
type singleflight struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	wg         sync.WaitGroup
	categories []Category
	err        error
}

func newSingleflight() *singleflight {
	return &singleflight{calls: map[string]*flightCall{}}
}

// do 对同 key 并发调用只执行一次 fn；所有调用方共享结果。
func (s *singleflight) do(key string, fn func() ([]Category, error)) ([]Category, error) {
	s.mu.Lock()
	if call, ok := s.calls[key]; ok {
		s.mu.Unlock()
		call.wg.Wait()
		return call.categories, call.err
	}
	call := &flightCall{}
	call.wg.Add(1)
	s.calls[key] = call
	s.mu.Unlock()

	call.categories, call.err = fn()
	call.wg.Done()

	s.mu.Lock()
	delete(s.calls, key)
	s.mu.Unlock()
	return call.categories, call.err
}
