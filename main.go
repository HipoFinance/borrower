package main

import (
	"borrower/borrower"
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// version is stamped at build time: go build -ldflags "-X main.version=v3.0.0". A binary built any
// other way reports "dev", so a log line always says whether it came from a release.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	log.Printf("🟢 Borrower %v started", version)

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
	processTimer := time.NewTimer(0)
	requestTimer := time.NewTimer(0)
	for {
		select {

		case <-stop:
			processTimer.Stop()
			requestTimer.Stop()
			return

		case <-processTimer.C:
			processWait := borrower.Process()
			if processWait <= 0 {
				processWait = 1 * time.Minute
			}
			// Add a 60 second jitter
			processWait = processWait.Round(time.Second) + time.Duration(rand.Intn(60))*time.Second
			until := time.Now().Add(processWait).Format(borrower.TimeFormat)
			log.Printf("💤 Next process of participations in %v at %v", processWait, until)
			processTimer.Reset(processWait)
			continue

		case <-requestTimer.C:
			requestWait := borrower.RequestLoan()
			if requestWait <= 0 {
				requestWait = 1 * time.Minute
			}
			requestWait = requestWait.Round(time.Second)
			until := time.Now().Add(requestWait).Format(borrower.TimeFormat)
			log.Printf("   💤 Next request in %v at %v", requestWait, until)
			requestTimer.Reset(requestWait)
			continue

		}
	}
}
