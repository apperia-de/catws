# Coinbase Advanced Trade WebSocket (catws)

![[Go Report Card](https://goreportcard.com/report/github.com/sknr/catws)](https://goreportcard.com/badge/github.com/sknr/catws)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/sknr/catws?style=flat)
![GitHub Licence](https://img.shields.io/github/license/sknr/catws)

This go package provides an easy accessible API for connecting 
and consuming the *Coinbase Advanced Trade WebSocket*.

> See [Advanced Trade WebSocket Overview](https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-overview) for more details.

## Installation

`go get -u github.com/sknr/catws`

## Preparation

In order to subscribe to channels and consume data from the _Coinbase Advanced Trade WebSocket_, you need to create an API key first.

Please visit [Creating Cloud API keys](https://docs.cloud.coinbase.com/advanced-trade-api/docs/auth#creating-cloud-api-keys) for details about creating an API key.

## Example usage

```go
package main

import (
	"fmt"
	"github.com/sknr/catws"
	"os"
	"os/signal"
	"time"
)

func main() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	ws := catws.New(
		catws.WithCredentials("YOUR_COINBASE_API_KEY", "YOUR_COINBASE_API_SECRET"),
		catws.WithLogging(), // Enable logging.
	)
	// Subscribe to user channel (no product_ids needed)
	ws.Subscribe(catws.UserChannel, nil)
	// Subscribe to Ticker batch updates for some product ids
	ws.Subscribe(catws.TickerBatchChannel, []string{"BTC-EUR", "ETH-EUR", "XRP-EUR"})
	quitChan := make(chan struct{})

	go func() {
		for {
			select {
			//case m := <-ws.Channel.Heartbeat:
			//	fmt.Println(m)
			case m := <-ws.Channel.Subscription:
				fmt.Println(m)
			case m := <-ws.Channel.User:
				fmt.Println(m)
			case m := <-ws.Channel.Ticker:
				fmt.Println(m)
			case m := <-ws.Channel.Status:
				fmt.Println(m)
			case m := <-ws.Channel.MarketTrades:
				fmt.Println(m)
			case m := <-ws.Channel.Level2:
				fmt.Println(m)
			case <-quitChan:
				fmt.Println("shutdown go-routine...")
				quitChan <- struct{}{}
				return
			}
		}
	}()

	// Block until a signal is received. (CTRL-C)
	<-c
	// Correctly unsubscribe from user channel, since only one connection per user is allowed.
	ws.Unsubscribe(catws.UserChannel, nil)
	time.Sleep(time.Second)
	quitChan <- struct{}{}
	// Wait for the go routine to quit.
	<-quitChan
	fmt.Println("closing websocket...")
	ws.CloseNormal()
}
```