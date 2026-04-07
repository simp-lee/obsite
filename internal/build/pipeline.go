package build

import (
	"runtime"
	"sync"
)

type pipelineJob[T any] struct {
	index int
	item  T
}

type pipelineResult[R any] struct {
	index int
	value R
	err   error
}

func runOrderedPipeline[T any, R any](items []T, concurrency int, fn func(T) (R, error)) ([]R, error) {
	if len(items) == 0 {
		return nil, nil
	}

	workerCount := normalizeConcurrency(concurrency)
	if workerCount > len(items) {
		workerCount = len(items)
	}

	jobs := make(chan pipelineJob[T])
	results := make(chan pipelineResult[R], len(items))

	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				value, err := fn(job.item)
				results <- pipelineResult[R]{
					index: job.index,
					value: value,
					err:   err,
				}
			}
		}()
	}

	go func() {
		for index, item := range items {
			jobs <- pipelineJob[T]{index: index, item: item}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	ordered := make([]R, len(items))
	var firstErr error
	for result := range results {
		if firstErr == nil && result.err != nil {
			firstErr = result.err
		}
		ordered[result.index] = result.value
	}

	if firstErr != nil {
		return ordered, firstErr
	}
	return ordered, nil
}

func normalizeConcurrency(concurrency int) int {
	if concurrency > 0 {
		return concurrency
	}
	if runtime.NumCPU() > 0 {
		return runtime.NumCPU()
	}
	return 1
}
