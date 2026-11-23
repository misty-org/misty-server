package app

import "context"

type Worker interface {
	Run(context.Context)
}

type WorkerFunc func(context.Context)

func (worker WorkerFunc) Run(ctx context.Context) {
	worker(ctx)
}

func startWorkers(ctx context.Context, workers ...Worker) {
	for _, worker := range workers {
		go worker.Run(ctx)
	}
}
