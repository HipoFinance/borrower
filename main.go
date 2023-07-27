package main

import (
	"borrower/borrower"
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	log.Println("🟢 Borrower started")

	stop, done := start()

	go func() {
		stopSignal := make(chan os.Signal, 1)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)
		s := <-stopSignal
		log.Printf("❗️ Got signal '%v', stopping", s)
		stop()
	}()

	<-done
	log.Println("🔴 Borrower stopped")
}

func start() (context.CancelFunc, <-chan struct{}) {
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		loop(ctx.Done())
	}()

	go func() {
		wg.Wait()
		close(done)
	}()

	return cancel, done
}

func loop(stop <-chan struct{}) {
	waitTime, _ := time.ParseDuration("1m")
	for {
		borrower.Process()

		select {
		case <-stop:
			return
		case <-time.After(waitTime):
			continue
		}
	}
}
