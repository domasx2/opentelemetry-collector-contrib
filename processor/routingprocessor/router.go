// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package routingprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/routingprocessor"

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl"
)

var errExporterNotFound = errors.New("exporter not found")

// router registers exporters and default exporters for an exporter. router can
// be instantiated with component.TracesExporter, component.MetricsExporter, and
// component.LogsExporter type arguments.
type router[E component.Exporter, K any] struct {
	logger *zap.Logger
	parser ottl.Parser[K]

	defaultExporterIDs []string
	table              []RoutingTableItem

	defaultExporters []E
	routes           map[string]routingItem[E, K]
}

// newRouter creates a new router instance with its type parameter constrained
// to component.Exporter.
func newRouter[E component.Exporter, K any](
	table []RoutingTableItem,
	defaultExporterIDs []string,
	settings component.TelemetrySettings,
	parser ottl.Parser[K],
) router[E, K] {
	return router[E, K]{
		logger: settings.Logger,
		parser: parser,

		table:              table,
		defaultExporterIDs: defaultExporterIDs,

		routes: make(map[string]routingItem[E, K]),
	}
}

type routingItem[E component.Exporter, K any] struct {
	exporters []E
	statement *ottl.Statement[K]
}

func (r *router[E, K]) registerExporters(available map[config.ComponentID]component.Exporter) error {
	// register default exporters
	err := r.registerDefaultExporters(available)
	if err != nil {
		return err
	}

	// register exporters for each route
	err = r.registerRouteExporters(available)
	if err != nil {
		return err
	}

	return nil
}

// registerDefaultExporters registers the configured default exporters
// using the provided available exporters map.
func (r *router[E, K]) registerDefaultExporters(available map[config.ComponentID]component.Exporter) error {
	for _, name := range r.defaultExporterIDs {
		e, err := r.extractExporter(name, available)
		if errors.Is(err, errExporterNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		r.defaultExporters = append(r.defaultExporters, e)
	}

	return nil
}

// registerRouteExporters registers route exporters using the provided
// available exporters map to check if they were available.
func (r *router[E, K]) registerRouteExporters(available map[config.ComponentID]component.Exporter) error {
	for _, item := range r.table {
		statement, err := r.getStatementFrom(item)
		if err != nil {
			return err
		}

		route, ok := r.routes[key(item)]
		if !ok {
			route.statement = statement
		}

		for _, name := range item.Exporters {
			e, err := r.extractExporter(name, available)
			if errors.Is(err, errExporterNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			route.exporters = append(route.exporters, e)
		}
		r.routes[key(item)] = route
	}
	return nil
}

// getStatementFrom builds a routing OTTL statements from provided
// routing table entry configuration. If routing table entry configuration
// does not contain a OTTL statement then nil is returned.
func (r *router[E, K]) getStatementFrom(item RoutingTableItem) (*ottl.Statement[K], error) {
	var statement *ottl.Statement[K]
	if item.Statement != "" {
		statements, err := r.parser.ParseStatements([]string{item.Statement})
		if err != nil {
			return statement, err
		}
		if len(statements) != 1 {
			return statement, errors.New("more than one statement specified")
		}
		statement = statements[0]
	}
	return statement, nil
}

func key(entry RoutingTableItem) string {
	if entry.Value != "" {
		return entry.Value
	}
	return entry.Statement
}

// extractExporter returns an exporter for the given name (type/name) and type
// argument if it exists in the list of available exporters.
func (r *router[E, K]) extractExporter(name string, available map[config.ComponentID]component.Exporter) (E, error) {
	var exporter E

	id, err := config.NewComponentIDFromString(name)
	if err != nil {
		return exporter, err
	}
	v, ok := available[id]
	if !ok {
		r.logger.Warn(
			"Can't find the exporter for the routing processor for this pipeline type."+
				" This is OK if you did not specify this processor for that pipeline type",
			zap.Any("pipeline_type", new(E)),
			zap.Error(
				fmt.Errorf(
					"error registering exporter %q",
					name,
				),
			),
		)
		return exporter, errExporterNotFound
	}
	exporter, ok = v.(E)
	if !ok {
		return exporter,
			fmt.Errorf("the exporter %q isn't a %T exporter", id.String(), new(E))
	}
	return exporter, nil
}

func (r *router[E, K]) getExporters(key string) []E {
	e, ok := r.routes[key]
	if !ok {
		return r.defaultExporters
	}
	return e.exporters
}
