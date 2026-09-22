package catalog

import "sync"

// parallel runs tasks with at most limit running at once and waits for all.
// Tasks record their own failures: catalog fan-outs degrade per item instead
// of failing the whole view.
func parallel(limit int, tasks ...func()) {
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			task()
		}()
	}
	wg.Wait()
}
