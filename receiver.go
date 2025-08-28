package influxdbreader

import (
	"github.com/henrikrexed/influxdbreader/receiver/influxdbreaderreceiver"
	"go.opentelemetry.io/collector/receiver"
)

// NewFactory returns the InfluxDB reader receiver factory
func NewFactory() receiver.Factory {
	return influxdbreaderreceiver.NewFactory()
}
