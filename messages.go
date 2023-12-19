package catws

import (
	"fmt"
	"strings"
	"time"
)

// SubscribeReq Request of the subscribe message
type SubscribeReq struct {
	Type       string   `json:"type"`        // Type of the message
	ProductIds []string `json:"product_ids"` // List of product ids
	Channel    string   `json:"channel"`     // The name of the channel
	JWT        string   `json:"jwt"`         // Each JWT is valid for 2 minutes
	Timestamp  string   `json:"timestamp"`   // Current timestamp of the message as a unix integer
}

// UnsubscribeReq Request of the unsubscribe message
type UnsubscribeReq struct {
	Type       string   `json:"type"`        // Type of the message
	ProductIds []string `json:"product_ids"` // List of product ids
	Channel    string   `json:"channel"`     // The name of the channel
	JWT        string   `json:"jwt"`         // Each JWT is valid for 2 minutes
	Timestamp  string   `json:"timestamp"`   // Current timestamp of the message as a unix integer
}

// Message a Websocket message
type Message struct {
	Channel     string    `json:"channel"`
	ClientID    string    `json:"client_id"`
	Timestamp   time.Time `json:"timestamp"`
	SequenceNum int       `json:"sequence_num"`
}

// SubscriptionsMessage Response of the subscribe message
type SubscriptionsMessage struct {
	Message
	Events []struct {
		Subscriptions map[string][]interface{} `json:"subscriptions"`
	} `json:"events"`
}

type HeartbeatMessage struct {
	Message
	Events []struct {
		CurrentTime      string `json:"current_time"`
		HeartbeatCounter string `json:"heartbeat_counter"`
	} `json:"events"`
}

// UserMessage is the type of the user message which is returned if subscribed to "user" channel
type UserMessage struct {
	Message
	Events []struct {
		Type   string `json:"type"`
		Orders []struct {
			OrderID            string    `json:"order_id"`
			ClientOrderID      string    `json:"client_order_id"`
			CumulativeQuantity string    `json:"cumulative_quantity"`
			LeavesQuantity     string    `json:"leaves_quantity"`
			AvgPrice           string    `json:"avg_price"`
			TotalFees          string    `json:"total_fees"`
			Status             string    `json:"status"`
			ProductID          string    `json:"product_id"`
			CreationTime       time.Time `json:"creation_time"`
			OrderSide          string    `json:"order_side"`
			OrderType          string    `json:"order_type"`
		} `json:"orders"`
	} `json:"events"`
}

func (m UserMessage) String() string {
	b := strings.Builder{}
	for _, e := range m.Events {
		b.WriteString("Orders:\n")
		for i, o := range e.Orders {
			b.WriteString(fmt.Sprintf("%d) Order[%s]: ProductID: %s | Side: %s | Type: %s | Status: %s \n", i+1, o.OrderID, o.ProductID, o.OrderSide, o.OrderType, o.Status))
		}
	}

	return b.String()
}

type StatusMessage struct {
	Message
	Events []struct {
		Type     string `json:"type"`
		Products []struct {
			ProductType    string `json:"product_type"`
			ID             string `json:"id"`
			BaseCurrency   string `json:"base_currency"`
			QuoteCurrency  string `json:"quote_currency"`
			BaseIncrement  string `json:"base_increment"`
			QuoteIncrement string `json:"quote_increment"`
			DisplayName    string `json:"display_name"`
			Status         string `json:"status"`
			StatusMessage  string `json:"status_message"`
			MinMarketFunds string `json:"min_market_funds"`
		} `json:"products"`
	} `json:"events"`
}

type TickerMessage struct {
	Message
	Events []struct {
		Type    string `json:"type"`
		Tickers []struct {
			Type               string `json:"type"`
			ProductID          string `json:"product_id"`
			Price              string `json:"price"`
			Volume24H          string `json:"volume_24_h"`
			Low24H             string `json:"low_24_h"`
			High24H            string `json:"high_24_h"`
			Low52W             string `json:"low_52_w"`
			High52W            string `json:"high_52_w"`
			PricePercentChg24H string `json:"price_percent_chg_24_h"`
		} `json:"tickers"`
	} `json:"events"`
}

type Level2Message struct {
	Message
	Events []struct {
		Type      string `json:"type"`
		ProductID string `json:"product_id"`
		Updates   []struct {
			Side        string    `json:"side"`
			EventTime   time.Time `json:"event_time"`
			PriceLevel  string    `json:"price_level"`
			NewQuantity string    `json:"new_quantity"`
		} `json:"updates"`
	} `json:"events"`
}

type MarketTradesMessage struct {
	Message
	Events []struct {
		Type   string `json:"type"`
		Trades []struct {
			TradeID   string    `json:"trade_id"`
			ProductID string    `json:"product_id"`
			Price     string    `json:"price"`
			Size      string    `json:"size"`
			Side      string    `json:"side"`
			Time      time.Time `json:"time"`
		} `json:"trades"`
	} `json:"events"`
}
