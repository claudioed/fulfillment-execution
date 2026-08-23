package observability

import (
	"context"
	"strings"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// KafkaHeaderCarrier adapts a kafka-go header slice to
// propagation.TextMapCarrier. kafka-go has no automatic instrumentation, so
// this is what actually carries trace context across the message boundary --
// from wes-work-planning's WorkReleased producer into this service's
// consumer, and on to this service's own TaskCompleted publisher.
//
// Headers is a pointer because Set must be able to append to the caller's
// slice, which a value receiver could not do.
type KafkaHeaderCarrier struct {
	Headers *[]kafkago.Header
}

// Get returns the first header matching key, case-insensitively (the W3C
// keys the propagator writes are lowercase, but a broker or another client
// may hand them back in any case).
func (c KafkaHeaderCarrier) Get(key string) string {
	if c.Headers == nil {
		return ""
	}
	for _, h := range *c.Headers {
		if strings.EqualFold(h.Key, key) {
			return string(h.Value)
		}
	}
	return ""
}

// Set replaces the value of an existing header with a matching key, or
// appends a new one.
func (c KafkaHeaderCarrier) Set(key, value string) {
	if c.Headers == nil {
		return
	}
	for i, h := range *c.Headers {
		if strings.EqualFold(h.Key, key) {
			(*c.Headers)[i].Value = []byte(value)
			return
		}
	}
	*c.Headers = append(*c.Headers, kafkago.Header{Key: key, Value: []byte(value)})
}

// Keys returns every header key present.
func (c KafkaHeaderCarrier) Keys() []string {
	if c.Headers == nil {
		return nil
	}
	keys := make([]string, 0, len(*c.Headers))
	for _, h := range *c.Headers {
		keys = append(keys, h.Key)
	}
	return keys
}

var _ propagation.TextMapCarrier = KafkaHeaderCarrier{}

// InjectKafkaTrace writes the trace context active in ctx into headers, to
// be published alongside the message body.
func InjectKafkaTrace(ctx context.Context, headers *[]kafkago.Header) {
	otel.GetTextMapPropagator().Inject(ctx, KafkaHeaderCarrier{Headers: headers})
}

// ExtractKafkaTrace returns ctx with the trace context found in headers, so
// the span started around handling this message becomes a child of the
// producer's span rather than the root of an unrelated trace.
func ExtractKafkaTrace(ctx context.Context, headers []kafkago.Header) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, KafkaHeaderCarrier{Headers: &headers})
}
