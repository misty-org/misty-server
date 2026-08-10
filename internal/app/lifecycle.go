package app

import "context"

// Worker is a background process owned by the application lifecycle.
type Worker interface {
	Run(context.Context)
}

// WorkerFunc adapts a function to Worker.
type WorkerFunc func(context.Context)

func (worker WorkerFunc) Run(ctx context.Context) {
	worker(ctx)
}

func startWorkers(ctx context.Context, workers ...Worker) {
	for _, worker := range workers {
		go worker.Run(ctx)
	}
}
