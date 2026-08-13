package bus

import (
	"context"
	"errors"
	"strings"
	"testing"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrBusUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestValidation(t *testing.T) {
	if result := Publish("", nil).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty topic err = %v", result.Err)
	}
}

func TestMessagesReportsValidationErrors(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  error
	}{
		{name: "empty", want: ErrInvalid},
		{name: "too large", topic: strings.Repeat("x", abi.BusMaxTopic+1), want: ErrTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			emitted := make(chan Message, 1)
			subscription := Messages("validation", test.topic, func(message Message) app.Event { return message })
			subscription[0].Start(context.Background(), func(event app.Event) {
				emitted <- event.(Message)
			})
			select {
			case message := <-emitted:
				if !errors.Is(message.Err, test.want) {
					t.Fatalf("message err = %v, want %v", message.Err, test.want)
				}
			default:
				t.Fatal("validation message was not emitted")
			}
		})
	}
}
