package influxdbreaderreceiver

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// mockHost is a mock implementation of component.Host for testing
type mockHost struct{}

func (m *mockHost) ReportFatalError(err error) {}

func (m *mockHost) GetFactory(kind component.Kind, componentType component.Type) component.Factory {
	return nil
}

func (m *mockHost) GetExtensions() map[component.ID]component.Component {
	return nil
}

func (m *mockHost) GetExporters() map[component.Type]map[component.ID]component.Component {
	return nil
}

// mockConsumer is a simple mock implementation for testing
type mockConsumer struct {
	metrics []pmetric.Metrics
}

func (m *mockConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (m *mockConsumer) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	m.metrics = append(m.metrics, md)
	return nil
}

func (m *mockConsumer) AllMetrics() []pmetric.Metrics {
	return m.metrics
}
