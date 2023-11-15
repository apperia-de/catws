package catws

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	backoff2 "github.com/cenkalti/backoff/v4"
	"io"
	"log"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	AdvanceTradeWebsocketURL = "wss://advanced-trade-ws.coinbase.com"

	HeartbeatChannel     = "heartbeats"
	UserChannel          = "user"
	StatusChannel        = "status"
	SubscriptionsChannel = "subscriptions"
	TickerChannel        = "ticker"
	TickerBatchChannel   = "ticker_batch"
	Level2Channel        = "level2"
	MarketTradesChannel  = "market_trades"

	ContextTimeout = time.Second * 10
)

type Option func(*AdvancedTradeWS)

func WithCredentials(apiKey, apiSecret string) Option {
	return func(atws *AdvancedTradeWS) {
		atws.credentials.apiKey = apiKey
		atws.credentials.apiSecret = apiSecret
	}
}

func WithLogging() Option {
	return func(atws *AdvancedTradeWS) {
		atws.logger = log.New(os.Stderr, "", log.Lmicroseconds)
	}
}

type AdvancedTradeWS struct {
	conn        *websocket.Conn
	wg          sync.WaitGroup
	logger      *log.Logger
	opts        []Option // Store options for reconnect
	credentials struct {
		apiKey    string
		apiSecret string
	}
	Channel struct {
		Heartbeat    <-chan HeartbeatMessage
		Level2       <-chan Level2Message
		MarketTrades <-chan MarketTradesMessage
		Status       <-chan StatusMessage
		Subscription <-chan SubscriptionsMessage
		Ticker       <-chan TickerMessage
		User         <-chan UserMessage
	}
}

func New(opts ...Option) *AdvancedTradeWS {
	atws := &AdvancedTradeWS{
		logger: log.New(io.Discard, "", log.LstdFlags),
		opts:   opts,
	}

	// Loop through each option
	for _, opt := range opts {
		opt(atws)
	}

	return connect(atws)
}

func (atws *AdvancedTradeWS) CloseNormal() {
	atws.Unsubscribe(UserChannel, nil)
	time.Sleep(time.Second)
	if err := atws.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		atws.logger.Fatal(err)
	}
	atws.wg.Wait()
}

func (atws *AdvancedTradeWS) Subscribe(channel string, productIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	msg := SubscribeReq{
		Type:       "subscribe",
		ProductIds: productIDs,
		Channel:    channel,
		APIKey:     atws.credentials.apiKey,
		Timestamp:  timestamp,
		Signature:  atws.signature(timestamp, channel, productIDs),
	}

	if len(productIDs) > 0 {
		atws.logger.Printf("Subscribe to %q channel for product ids: [%s]", channel, strings.Join(productIDs, ","))
	} else {
		atws.logger.Printf("Subscribe to %q channel", channel)
	}

	err := wsjson.Write(ctx, atws.conn, msg)
	if err != nil {
		// ...
		panic(err)
	}
}

func (atws *AdvancedTradeWS) Unsubscribe(channel string, productIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	msg := UnsubscribeReq{
		Type:       "unsubscribe",
		ProductIds: productIDs,
		Channel:    channel,
		APIKey:     atws.credentials.apiKey,
		Timestamp:  timestamp,
		Signature:  atws.signature(timestamp, channel, productIDs),
	}

	atws.logger.Printf("Unsubscribe from %q channel", channel)

	err := wsjson.Write(ctx, atws.conn, msg)
	if err != nil {
		// ...
		panic(err)
	}
}

func connect(atws *AdvancedTradeWS) *AdvancedTradeWS {
	err := backoff2.RetryNotify(atws.connect, backoff2.NewExponentialBackOff(), func(err error, t time.Duration) {
		atws.logger.Println(err)
		atws.logger.Println("Next reconnection try at %s", time.Now().Add(t))
	})

	if err != nil {
		// Max reconnection tries reached -> exit
		panic(err)
	}

	// We always subscribe to the heartbeat channel, in order to keep the connection open
	atws.Subscribe(HeartbeatChannel, nil)

	go atws.readMessages()

	return atws
}

func (atws *AdvancedTradeWS) signature(timestamp, channel string, productIDs []string) string {
	payload := fmt.Sprintf("%s%s%s", timestamp, channel, strings.Join(productIDs, ","))

	sig := hmac.New(sha256.New, []byte(atws.credentials.apiSecret))
	sig.Write([]byte(payload))
	sum := hex.EncodeToString(sig.Sum(nil))

	return sum
}

func (atws *AdvancedTradeWS) connect() error {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()

	//c, _, err := websocket.Dial(ctx, AdvanceTradeWebsocketURL, &websocket.DialOptions{HTTPClient: &http.Client{Timeout: 30}})
	c, _, err := websocket.Dial(ctx, AdvanceTradeWebsocketURL, nil)
	if err != nil {
		return err
	}
	// In order to disable read limit set it to -1
	c.SetReadLimit(1 << 20) // Equals 2^20 = 1048576 bytes = 1MB

	atws.conn = c

	return nil
}

func (atws *AdvancedTradeWS) reconnect() {
	_ = atws.conn.CloseNow()
	atws.wg.Wait()
	atws.logger.Printf("Reconnecting...")
	atws = New(atws.opts...)
}

// readMessages reads message from the subscribed websocket channels and sends it to the corresponding messageChan
func (atws *AdvancedTradeWS) readMessages() {
	var res interface{}

	atws.wg.Add(1)
	defer atws.wg.Done()

	heartbeatChan := make(chan HeartbeatMessage, 1)
	atws.Channel.Heartbeat = func(ch chan HeartbeatMessage) <-chan HeartbeatMessage {
		return ch
	}(heartbeatChan)

	statusChan := make(chan StatusMessage, 5)
	atws.Channel.Status = func(ch chan StatusMessage) <-chan StatusMessage {
		return ch
	}(statusChan)

	userChan := make(chan UserMessage, 5)
	atws.Channel.User = func(ch chan UserMessage) <-chan UserMessage {
		return ch
	}(userChan)

	tickerChan := make(chan TickerMessage, 50)
	atws.Channel.Ticker = func(ch chan TickerMessage) <-chan TickerMessage {
		return ch
	}(tickerChan)

	level2Chan := make(chan Level2Message, 100)
	atws.Channel.Level2 = func(ch chan Level2Message) <-chan Level2Message {
		return ch
	}(level2Chan)

	marketTradesChan := make(chan MarketTradesMessage, 50)
	atws.Channel.MarketTrades = func(ch chan MarketTradesMessage) <-chan MarketTradesMessage {
		return ch
	}(marketTradesChan)

	subscriptionChan := make(chan SubscriptionsMessage, 10)
	atws.Channel.Subscription = func(ch chan SubscriptionsMessage) <-chan SubscriptionsMessage {
		return ch
	}(subscriptionChan)

	for {
		if err := wsjson.Read(context.Background(), atws.conn, &res); err != nil {
			switch websocket.CloseStatus(err) {
			case websocket.StatusNormalClosure:
				atws.logger.Println("Received normal close message")
			case websocket.StatusAbnormalClosure:
				atws.logger.Println("Abnormal closure -> Restart websocket")
				go atws.reconnect()
			case -1:
				// Not a CloseError
				atws.logger.Printf("Not a CloseError: %s\n", err)
				go atws.reconnect()
			}

			return
		}

		var msg Message
		data, _ := json.Marshal(&res)
		_ = json.Unmarshal(data, &msg)

		switch msg.Channel {
		case HeartbeatChannel:
			var m HeartbeatMessage
			_ = json.Unmarshal(data, &m)
			if len(heartbeatChan) == cap(heartbeatChan) {
				<-heartbeatChan // Discard oldest message
			}
			heartbeatChan <- m
		case UserChannel:
			var m UserMessage
			_ = json.Unmarshal(data, &m)
			if len(userChan) == cap(userChan) {
				<-userChan // Discard oldest message
			}
			userChan <- m
		case StatusChannel:
			var m StatusMessage
			_ = json.Unmarshal(data, &m)
			if len(statusChan) == cap(statusChan) {
				<-statusChan // Discard oldest message
			}
			statusChan <- m
		case SubscriptionsChannel:
			var m SubscriptionsMessage
			_ = json.Unmarshal(data, &m)
			if len(subscriptionChan) == cap(subscriptionChan) {
				<-subscriptionChan // Discard oldest message
			}
			subscriptionChan <- m
		case TickerChannel, TickerBatchChannel:
			var m TickerMessage
			_ = json.Unmarshal(data, &m)
			if len(tickerChan) == cap(tickerChan) {
				<-tickerChan // Discard oldest message
			}
			tickerChan <- m
		case Level2Channel:
			var m Level2Message
			_ = json.Unmarshal(data, &m)
			if len(level2Chan) == cap(level2Chan) {
				<-level2Chan // Discard oldest message
			}
			level2Chan <- m
		case MarketTradesChannel:
			var m MarketTradesMessage
			_ = json.Unmarshal(data, &m)
			if len(marketTradesChan) == cap(marketTradesChan) {
				<-marketTradesChan // Discard oldest message
			}
			marketTradesChan <- m
		default:
			atws.logger.Println("Unknown message:", string(data))
		}
	}
}
