package httpapi

import (
	"errors"
	"testing"
)

func TestSubscriptionNotifyErrorCategory(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: "none"},
		{name: "signature", err: errors.New("invalid alipay sign"), want: "signature"},
		{name: "amount", err: errors.New("payment money mismatch"), want: "amount"},
		{name: "order", err: errors.New("order not found"), want: "order"},
		{name: "status", err: errors.New("payment is not successful"), want: "status"},
		{name: "grant", err: errors.New("owner id is required"), want: "grant"},
		{name: "system", err: errors.New("database unavailable"), want: "system"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subscriptionNotifyErrorCategory(tt.err); got != tt.want {
				t.Fatalf("subscriptionNotifyErrorCategory() = %q, want %q", got, tt.want)
			}
		})
	}
}
