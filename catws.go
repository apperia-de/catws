package catws

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
	"io"
	"log"
	"math"
	"math/big"
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
	Level2Channel        = "level2"
	MarketTradesChannel  = "market_trades"
	StatusChannel        = "status"
	SubscriptionsChannel = "subscriptions"
	TickerBatchChannel   = "ticker_batch"
	TickerChannel        = "ticker"
	UserChannel          = "user"

	ContextTimeout = time.Second * 10
	JWTServiceName = "public_websocket_api"
)

type nonceSource struct{}

func (n nonceSource) Nonce() (string, error) {
	r, err := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return "", err
	}
	return r.String(), nil
}

type Option func(*AdvancedTradeWS)

// WithCredentials option provides the required apiKey and apiSecret of the coinbase user
func WithCredentials(apiKey, apiSecret string) Option {
	return func(ws *AdvancedTradeWS) {
		ws.credentials.apiKey = apiKey
		ws.credentials.apiSecret = apiSecret
	}
}

// WithURL allows changing the default coinbase websocket url
func WithURL(url string) Option {
	return func(ws *AdvancedTradeWS) {
		ws.wsURL = url
	}
}

// WithLogging option enables package logging to stdErr (Default: logging to io.Discard)
func WithLogging() Option {
	return func(ws *AdvancedTradeWS) {
		ws.logger = log.New(os.Stderr, "", log.Lmicroseconds)
	}
}

type AdvancedTradeWS struct {
	conn        *websocket.Conn
	wg          sync.WaitGroup
	logger      *log.Logger
	opts        []Option // Store options for reconnect
	wsURL       string
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
	ws := &AdvancedTradeWS{
		logger: log.New(io.Discard, "", log.LstdFlags),
		opts:   opts,
		wsURL:  AdvanceTradeWebsocketURL,
	}

	// Loop through each option
	for _, opt := range opts {
		opt(ws)
	}

	return connect(ws)
}

func (ws *AdvancedTradeWS) CloseNormal() {
	if err := ws.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		ws.logger.Fatal(err)
	}
	ws.wg.Wait()
}

func (ws *AdvancedTradeWS) Subscribe(channel string, productIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	msg := SubscribeReq{
		Type:       "subscribe",
		ProductIds: productIDs,
		Channel:    channel,
		JWT:        ws.buildJWT(),
		Timestamp:  timestamp,
	}

	if len(productIDs) > 0 {
		ws.logger.Printf("Subscribe to %q channel for product ids: [%s]", channel, strings.Join(productIDs, ","))
	} else {
		ws.logger.Printf("Subscribe to %q channel", channel)
	}

	err := wsjson.Write(ctx, ws.conn, msg)
	if err != nil {
		// ...
		panic(err)
	}
}

func (ws *AdvancedTradeWS) Unsubscribe(channel string, productIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)

	msg := UnsubscribeReq{
		Type:       "unsubscribe",
		ProductIds: productIDs,
		Channel:    channel,
		JWT:        ws.buildJWT(),
		Timestamp:  timestamp,
	}

	ws.logger.Printf("Unsubscribe from %q channel", channel)

	err := wsjson.Write(ctx, ws.conn, msg)
	if err != nil {
		// ...
		panic(err)
	}
}

func connect(ws *AdvancedTradeWS) *AdvancedTradeWS {
	err := backoff.RetryNotify(ws.connect, backoff.NewExponentialBackOff(), func(err error, t time.Duration) {
		ws.logger.Print(err)
		ws.logger.Printf("Next reconnection try at %s", time.Now().Add(t))
	})

	if err != nil {
		// Max reconnection tries reached -> exit
		panic(err)
	}

	// We always subscribe to the heartbeat channel, in order to keep the connection open
	ws.Subscribe(HeartbeatChannel, nil)

	go ws.readMessages()

	return ws
}

// buildJWT creates an JWT for authenticate API requests.
func (ws *AdvancedTradeWS) buildJWT() string {
	block, _ := pem.Decode([]byte(ws.credentials.apiSecret))
	if block == nil {
		panic("jwt: Could not decode private key")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		panic(fmt.Errorf("jwt: %w", err))
	}

	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: key},
		(&jose.SignerOptions{NonceSource: nonceSource{}}).WithType("JWT").WithHeader("kid", ws.credentials.apiKey),
	)
	if err != nil {
		panic(fmt.Errorf("jwt: %w", err))
	}

	cl := &jwt.Claims{
		Subject:   ws.credentials.apiKey,
		Issuer:    "coinbase-cloud",
		NotBefore: jwt.NewNumericDate(time.Now()),
		Expiry:    jwt.NewNumericDate(time.Now().Add(1 * time.Minute)),
		Audience:  jwt.Audience{JWTServiceName},
	}

	jwtString, err := jwt.Signed(sig).Claims(cl).CompactSerialize()
	if err != nil {
		panic(fmt.Errorf("jwt: %w", err))
	}
	return jwtString
}

func (ws *AdvancedTradeWS) connect() error {
	ctx, cancel := context.WithTimeout(context.Background(), ContextTimeout)
	defer cancel()

	c, _, err := websocket.Dial(ctx, ws.wsURL, nil)
	if err != nil {
		return err
	}

	// In order to disable read limit set it to -1
	c.SetReadLimit(1 << 20) // Equals 2^20 = 1048576 bytes = 1MB

	ws.conn = c

	return nil
}

func (ws *AdvancedTradeWS) reconnect() {
	_ = ws.conn.CloseNow()
	ws.wg.Wait()
	ws.logger.Printf("Reconnecting...")
	*ws = *New(ws.opts...)
}

// readMessages reads message from the subscribed websocket channels and sends it to the corresponding messageChan
func (ws *AdvancedTradeWS) readMessages() {
	var res interface{}

	ws.wg.Add(1)
	defer ws.wg.Done()

	heartbeatChan := make(chan HeartbeatMessage, 1)
	ws.Channel.Heartbeat = func(ch chan HeartbeatMessage) <-chan HeartbeatMessage {
		return ch
	}(heartbeatChan)

	statusChan := make(chan StatusMessage, 5)
	ws.Channel.Status = func(ch chan StatusMessage) <-chan StatusMessage {
		return ch
	}(statusChan)

	userChan := make(chan UserMessage, 5)
	ws.Channel.User = func(ch chan UserMessage) <-chan UserMessage {
		return ch
	}(userChan)

	tickerChan := make(chan TickerMessage, 50)
	ws.Channel.Ticker = func(ch chan TickerMessage) <-chan TickerMessage {
		return ch
	}(tickerChan)

	level2Chan := make(chan Level2Message, 100)
	ws.Channel.Level2 = func(ch chan Level2Message) <-chan Level2Message {
		return ch
	}(level2Chan)

	marketTradesChan := make(chan MarketTradesMessage, 50)
	ws.Channel.MarketTrades = func(ch chan MarketTradesMessage) <-chan MarketTradesMessage {
		return ch
	}(marketTradesChan)

	subscriptionChan := make(chan SubscriptionsMessage, 10)
	ws.Channel.Subscription = func(ch chan SubscriptionsMessage) <-chan SubscriptionsMessage {
		return ch
	}(subscriptionChan)

	for {
		if err := wsjson.Read(context.Background(), ws.conn, &res); err != nil {
			switch websocket.CloseStatus(err) {
			case websocket.StatusNormalClosure:
				ws.logger.Print("Received normal close message")
			case websocket.StatusAbnormalClosure:
				ws.logger.Print("Abnormal closure -> Restart websocket")
				go ws.reconnect()
			case -1:
				// Not a CloseError
				ws.logger.Printf("Not a CloseError: %s\n", err)
				go ws.reconnect()
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
			discardOldest(heartbeatChan)
			heartbeatChan <- m
		case UserChannel:
			var m UserMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(userChan)
			userChan <- m
		case StatusChannel:
			var m StatusMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(statusChan)
			statusChan <- m
		case SubscriptionsChannel:
			var m SubscriptionsMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(subscriptionChan)
			subscriptionChan <- m
		case TickerChannel, TickerBatchChannel:
			var m TickerMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(tickerChan)
			tickerChan <- m
		case Level2Channel:
			var m Level2Message
			_ = json.Unmarshal(data, &m)
			discardOldest(level2Chan)
			level2Chan <- m
		case MarketTradesChannel:
			var m MarketTradesMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(marketTradesChan)
			marketTradesChan <- m
		default:
			ws.logger.Print("Unknown message:", string(data))
		}
	}
}

// discardOldest removes the oldest message from the channel if the capacity of the channel is reached
func discardOldest[T any](c chan T) {
	if len(c) == cap(c) {
		<-c // Discard oldest message
	}
}
