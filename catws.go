package catws

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff/v4"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"log"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	AdvanceTradeWebsocketURL = "wss://advanced-trade-ws.coinbase.com"

	HeartbeatChannel    = "heartbeats"    // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#heartbeats-channel
	CandlesChannel      = "candles"       // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#candles-channel
	MarketTradesChannel = "market_trades" // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#market-trades-channel
	StatusChannel       = "status"        // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#status-channel
	TickerChannel       = "ticker"        // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#ticker-channel
	TickerBatchChannel  = "ticker_batch"  // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#ticker-batch-channel
	Level2Channel       = "level2"        // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#level2-channel
	UserChannel         = "user"          // https://docs.cloud.coinbase.com/advanced-trade-api/docs/ws-channels#user-channel

	emptyChannel = "" // Channel might be empty if an error occurs

	SubscriptionsChannel = "subscriptions"

	ContextTimeout     = 5 * time.Second
	ContextReadTimeout = 10 * time.Second

	JWTAudience = "public_websocket_api"
	JWTIssuer   = "coinbase-cloud"
)

type Option func(*AdvancedTradeWS)

// WithCredentials option provides the required apiKey and apiSecret of the coinbase user.
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

// WithSubscriptions sets a combination of channel and productIDs the user wants to subscribe to.
func WithSubscriptions(s map[string][]string) Option {
	for channel := range s {
		if !isAllowedChannel(channel) {
			panic(fmt.Sprintf("unkown channel %q", channel))
		}
	}

	return func(ws *AdvancedTradeWS) {
		ws.subscriptions = s
	}
}

// WithLogging option enables package logging to stdErr (Default: logging to io.Discard)
func WithLogging() Option {
	return func(ws *AdvancedTradeWS) {
		ws.logger = log.New(os.Stderr, "", log.Lmicroseconds|log.Lshortfile)
	}
}

// WithMaxElapsedTime sets the MaxElapsedTime after which the exponential backoff
// retries will finally stop trying to reconnect. Defaults to 15 minutes.
// Set to 0 in order to deactivate and let it retry forever.
func WithMaxElapsedTime(maxElapsedTime time.Duration) Option {
	return func(ws *AdvancedTradeWS) {
		ws.maxElapsedTime = maxElapsedTime
	}
}

type AdvancedTradeWS struct {
	conn        *websocket.Conn
	mu          sync.Mutex
	logger      *log.Logger
	opts        []Option // Store options for reconnect
	wsURL       string
	credentials struct {
		apiKey    string
		apiSecret string
	}
	subscriptions  map[string][]string // A map of channel to productIDs
	maxElapsedTime time.Duration       // Max reconnect time elapsed before the application stops
	active         bool
	ctx            context.Context
	cancelFunc     context.CancelFunc
	Channel        struct {
		Heartbeat    <-chan HeartbeatMessage
		Candles      <-chan CandlesMessage
		Status       <-chan StatusMessage
		MarketTrades <-chan MarketTradesMessage
		Ticker       <-chan TickerMessage
		Level2       <-chan Level2Message
		User         <-chan UserMessage
	}
	channel struct {
		heartbeat    chan HeartbeatMessage
		candles      chan CandlesMessage
		status       chan StatusMessage
		marketTrades chan MarketTradesMessage
		ticker       chan TickerMessage
		level2       chan Level2Message
		user         chan UserMessage
	}
}

func New(opts ...Option) *AdvancedTradeWS {
	ws := &AdvancedTradeWS{
		logger:         log.New(io.Discard, "", log.LstdFlags),
		opts:           opts,
		active:         true,
		wsURL:          AdvanceTradeWebsocketURL,
		subscriptions:  make(map[string][]string),
		maxElapsedTime: 15 * time.Minute, // Default time before not trying to reconnect anymore.
	}

	// Initialize available channels
	ws.channel.heartbeat = make(chan HeartbeatMessage, 1)
	ws.Channel.Heartbeat = ws.channel.heartbeat

	ws.channel.status = make(chan StatusMessage, 5)
	ws.Channel.Status = ws.channel.status

	ws.channel.user = make(chan UserMessage, 5)
	ws.Channel.User = ws.channel.user

	ws.channel.ticker = make(chan TickerMessage, 50)
	ws.Channel.Ticker = ws.channel.ticker

	ws.channel.ticker = make(chan TickerMessage, 50)
	ws.Channel.Ticker = ws.channel.ticker

	ws.channel.level2 = make(chan Level2Message, 100)
	ws.Channel.Level2 = ws.channel.level2

	ws.channel.marketTrades = make(chan MarketTradesMessage, 50)
	ws.Channel.MarketTrades = ws.channel.marketTrades

	ws.channel.candles = make(chan CandlesMessage, 50)
	ws.Channel.Candles = ws.channel.candles

	// Loop through each option
	for _, opt := range opts {
		opt(ws)
	}

	return ws
}

func (ws *AdvancedTradeWS) Connect() error {
	bOff := backoff.NewExponentialBackOff()
	bOff.MaxElapsedTime = ws.maxElapsedTime
	// bOffWithRetries := backoff.WithMaxRetries(bOff, 5)

	err := backoff.RetryNotify(ws.connect, bOff, func(err error, t time.Duration) {
		ws.logger.Print(err)
		ws.logger.Printf("Next reconnection try at %s", time.Now().Add(t))
	})

	return err
}

func (ws *AdvancedTradeWS) CloseNormal() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn == nil {
		// Ignore, since the connection has not been established yet
		return nil
	}
	return ws.conn.Close(websocket.StatusNormalClosure, "")
}

func (ws *AdvancedTradeWS) Subscribe(channel string, productIDs []string) {
	if !isAllowedChannel(channel) {
		panic(fmt.Sprintf("subscribe error: unsupported channel %q", channel))
	}

	msg := SubscribeReq{
		Type:       "subscribe",
		ProductIds: productIDs,
		Channel:    channel,
		JWT:        ws.buildJWT(),
		Timestamp:  getUnixTimestamp(),
	}

	ws.writeJsonMessage(msg)
	ws.logger.Printf("subscribe to channel %s", msg.Channel)
}

func (ws *AdvancedTradeWS) Unsubscribe(channel string, productIDs []string) {
	if _, ok := ws.subscriptions[channel]; !ok {
		panic(fmt.Sprintf("unsubscribe error: unsupported or not subscribed channel %q", channel))
	}

	msg := UnsubscribeReq{
		Type:       "unsubscribe",
		ProductIds: productIDs,
		Channel:    channel,
		JWT:        ws.buildJWT(),
		Timestamp:  getUnixTimestamp(),
	}

	ws.writeJsonMessage(msg)
	ws.logger.Printf("unsubscribe from channel %s", msg.Channel)
}

// buildJWT creates an JWT for authenticate API requests.
func (ws *AdvancedTradeWS) buildJWT() string {
	token := &jwt.Token{
		Header: map[string]interface{}{
			"typ":   "JWT",
			"alg":   jwt.SigningMethodES256.Alg(),
			"kid":   ws.credentials.apiKey,
			"nonce": getUnixTimestamp(),
		},
		Claims: jwt.MapClaims{
			"sub": ws.credentials.apiKey,
			"iss": JWTIssuer,
			"nbf": jwt.NewNumericDate(time.Now()),
			"exp": jwt.NewNumericDate(time.Now().Add(1 * time.Minute)),
			"aud": JWTAudience,
		},
		Method: jwt.SigningMethodES256,
	}

	key, err := jwt.ParseECPrivateKeyFromPEM([]byte(ws.credentials.apiSecret))
	if err != nil {
		panic(fmt.Errorf("jwt: %w", err))
	}

	jwtString, err := token.SignedString(key)
	if err != nil {
		panic(fmt.Errorf("jwt: %w", err))
	}

	return jwtString
}

func (ws *AdvancedTradeWS) connect() error {
	var err error
	ws.mu.Lock()
	defer ws.mu.Unlock()

	ws.ctx, ws.cancelFunc = context.WithCancel(context.Background())
	ctx, cancel := context.WithTimeout(ws.ctx, ContextTimeout)
	defer cancel()

	ws.conn, _, err = websocket.Dial(ctx, ws.wsURL, nil)
	if err != nil {
		return err
	}

	// In order to disable read limit set it to -1
	ws.conn.SetReadLimit(1 << 20) // Equals 2^20 = 1048576 bytes = 1MB

	go func() {
		<-ws.ctx.Done()
		// Reconnect on error
		go ws.reConnect()
	}()

	go ws.readMessages()

	// Subscribe to channels
	for channel, productIDs := range ws.subscriptions {
		ws.Subscribe(channel, productIDs)
	}

	// We always subscribe to the heartbeat channel, in order to keep the connection open
	if _, ok := ws.subscriptions[HeartbeatChannel]; !ok {
		ws.Subscribe(HeartbeatChannel, nil)
	}

	return nil
}

// readMessages reads message from the subscribed websocket channels and sends it to the corresponding messageChan
func (ws *AdvancedTradeWS) readMessages() {
	var (
		res    interface{}
		ctx    context.Context
		cancel context.CancelFunc
	)
	defer func() { ws.logger.Print("Exiting readMessages go routine") }()

	for {
		ctx, cancel = context.WithTimeout(ws.ctx, ContextReadTimeout)

		if err := wsjson.Read(ctx, ws.conn, &res); err != nil {
			switch websocket.CloseStatus(err) {
			case websocket.StatusNormalClosure:
				ws.logger.Print("Received normal close message")
			case websocket.StatusAbnormalClosure:
				ws.logger.Print("Abnormal closure -> Restart websocket")
				ws.cancelFunc()
			case -1:
				// Not a CloseError
				ws.logger.Printf("Not a CloseError: %s\n", err)
				ws.cancelFunc()
			default:
				ws.logger.Printf("CloseStatus: %s - Error: %s\n", websocket.CloseStatus(err), err)
				ws.cancelFunc()
			}

			cancel()
			return
		}

		var msg Message
		data, _ := json.Marshal(&res)
		_ = json.Unmarshal(data, &msg)

		switch msg.Channel {
		case HeartbeatChannel:
			var m HeartbeatMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.heartbeat)
			ws.channel.heartbeat <- m
		case CandlesChannel:
			var m CandlesMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.candles)
			ws.channel.candles <- m
		case UserChannel:
			var m UserMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.user)
			ws.channel.user <- m
		case StatusChannel:
			var m StatusMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.status)
			ws.channel.status <- m
		case TickerChannel, TickerBatchChannel:
			var m TickerMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.ticker)
			ws.channel.ticker <- m
		case Level2Channel:
			var m Level2Message
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.level2)
			ws.channel.level2 <- m
		case MarketTradesChannel:
			var m MarketTradesMessage
			_ = json.Unmarshal(data, &m)
			discardOldest(ws.channel.marketTrades)
			ws.channel.marketTrades <- m
		case SubscriptionsChannel:
			var m SubscriptionsMessage
			_ = json.Unmarshal(data, &m)
			ws.logger.Printf("SubscriptionsMessage: %v", m)
		case emptyChannel:
			var m struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			}
			_ = json.Unmarshal(data, &m)
			if m.Type == "error" {
				ws.logger.Printf("message error: %s", m.Message)
				ws.cancelFunc()

				cancel()
				return
			}
			ws.logger.Print(data)
		default:
			ws.logger.Printf("unknown channel %q with message: %s", msg.Channel, data)
		}
	}
}

func (ws *AdvancedTradeWS) reConnect() {
	if err := ws.Connect(); err != nil {
		panic("max reconnect tries reached")
	}
}

func (ws *AdvancedTradeWS) writeJsonMessage(msg interface{}) {
	if ws.conn == nil {
		ws.logger.Print("cannot write a message before connecting")
		return
	}

	ctx, cancel := context.WithTimeout(ws.ctx, ContextTimeout)
	defer cancel()

	if err := wsjson.Write(ctx, ws.conn, msg); err != nil {
		ws.logger.Print(err)
		ws.cancelFunc()
	}
}

// discardOldest removes the oldest message from the channel if the capacity of the channel is reached
func discardOldest[T any](c chan T) {
	if len(c) == cap(c) {
		<-c // Discard oldest message
	}
}

func getUnixTimestamp() string {
	return strconv.FormatInt(time.Now().UTC().Unix(), 10)
}

// isAllowedChannel returns true if the given channel is one of the allowed channels
func isAllowedChannel(channel string) bool {
	var allowedChannels = []string{HeartbeatChannel, CandlesChannel, MarketTradesChannel, StatusChannel, TickerChannel, TickerBatchChannel, Level2Channel, UserChannel}
	return slices.Contains(allowedChannels, channel)
}
