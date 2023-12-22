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
		catws.WithSubscriptions(map[string][]string{ // Set default subscriptions -> will be also established after reconnect.
			catws.UserChannel:    nil,
			catws.CandlesChannel: {"BTC-EUR"},
		}),
		catws.WithMaxElapsedTime(time.Hour), // Retry reconnect with exponential backoff for at most 1hour before exiting the app.
	)
	// Subscribe to Ticker batch updates for some product ids
	ws.Subscribe(catws.TickerBatchChannel, []string{"BTC-EUR", "ETH-EUR", "XRP-EUR"}) // Will not be established after reconnect
	closeChan := make(chan struct{})

	go func() {
		for {
			select {
			//case m := <-ws.Channel.Heartbeat:
			//	fmt.Println(m)
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
			case <-closeChan:
				fmt.Println("shutdown go-routine...")
				return
			}
		}
	}()

	// Block until a signal is received. (CTRL-C)
	<-c
	closeChan <- struct{}{}
	fmt.Println("closing websocket...")
	ws.CloseNormal()
}
```