// Copyright 2019-2022 The Inspektor Gadget authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ebpf

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	gadgetv1alpha1 "github.com/kinvolk/inspektor-gadget/pkg/apis/gadget/v1alpha1"
	"github.com/kinvolk/inspektor-gadget/pkg/bpfstats"
	"github.com/kinvolk/inspektor-gadget/pkg/gadget-collection/gadgets"
	ebpftoptracer "github.com/kinvolk/inspektor-gadget/pkg/gadgets/top/ebpf/tracer"
	"github.com/kinvolk/inspektor-gadget/pkg/gadgets/top/ebpf/types"

	log "github.com/sirupsen/logrus"
)

type Trace struct {
	helpers gadgets.GadgetHelpers

	traceName string
	node      string
	started   bool

	tracer *ebpftoptracer.Tracer
}

type TraceFactory struct {
	gadgets.BaseFactory
}

func NewFactory() gadgets.TraceFactory {
	return &TraceFactory{
		BaseFactory: gadgets.BaseFactory{DeleteTrace: deleteTrace},
	}
}

func (f *TraceFactory) Description() string {
	t := `ebpftop shows cpu time used by ebpf programs.

The following parameters are supported:
 - %s: Output interval, in seconds. (default %d)
 - %s: Maximum rows to print. (default %d)
 - %s: The field to sort the results by (%s). (default %s)`
	return fmt.Sprintf(t, types.IntervalParam, types.IntervalDefault,
		types.MaxRowsParam, types.MaxRowsDefault,
		types.SortByParam, strings.Join(types.SortBySlice, ","), types.SortByDefault)
}

func (f *TraceFactory) OutputModesSupported() map[string]struct{} {
	return map[string]struct{}{
		"Stream": {},
	}
}

func deleteTrace(name string, t interface{}) {
	trace := t.(*Trace)
	if trace.started {
		trace.stop()
	}
}

func (f *TraceFactory) Operations() map[string]gadgets.TraceOperation {
	n := func() interface{} {
		return &Trace{
			helpers: f.Helpers,
		}
	}

	return map[string]gadgets.TraceOperation{
		"start": {
			Doc: "Start ebpftop gadget",
			Operation: func(name string, trace *gadgetv1alpha1.Trace) {
				f.LookupOrCreate(name, n).(*Trace).Start(trace)
			},
		},
		"stop": {
			Doc: "Stop ebpftop gadget",
			Operation: func(name string, trace *gadgetv1alpha1.Trace) {
				f.LookupOrCreate(name, n).(*Trace).Stop(trace)
			},
		},
	}
}

func (t *Trace) Start(trace *gadgetv1alpha1.Trace) {
	if t.started {
		trace.Status.State = "Started"
		return
	}

	t.traceName = gadgets.TraceName(trace.ObjectMeta.Namespace, trace.ObjectMeta.Name)
	t.node = trace.Spec.Node

	maxRows := types.MaxRowsDefault
	intervalSeconds := types.IntervalDefault
	sortBy := types.SortByDefault

	if trace.Spec.Parameters != nil {
		params := trace.Spec.Parameters
		var err error

		if val, ok := params[types.MaxRowsParam]; ok {
			maxRows, err = strconv.Atoi(val)
			if err != nil {
				trace.Status.OperationError = fmt.Sprintf("%q is not valid for %s: %v", val, types.MaxRowsParam, err)
				return
			}
		}

		if val, ok := params[types.IntervalParam]; ok {
			intervalSeconds, err = strconv.Atoi(val)
			if err != nil {
				trace.Status.OperationError = fmt.Sprintf("%q is not valid for %s: %v", val, types.IntervalParam, err)
				return
			}
		}

		if val, ok := params[types.SortByParam]; ok {
			sortBy, err = types.ParseSortBy(val)
			if err != nil {
				trace.Status.OperationError = fmt.Sprintf("%q is not valid for %s: %v", val, types.SortByParam, err)
				return
			}
		}
	}

	config := &ebpftoptracer.Config{
		MaxRows:  maxRows,
		Interval: time.Second * time.Duration(intervalSeconds),
		SortBy:   sortBy,
	}

	eventCallback := func(ev *types.Event) {
		r, err := json.Marshal(ev)
		if err != nil {
			log.Warnf("Gadget %s: Failed to marshal event: %s", trace.Spec.Gadget, err)
			return
		}
		t.helpers.PublishEvent(t.traceName, string(r))
	}

	tracer, err := ebpftoptracer.NewTracer(config, eventCallback)
	if err != nil {
		trace.Status.OperationError = fmt.Sprintf("failed to create tracer: %s", err)
		return
	}

	t.tracer = tracer
	t.started = true

	trace.Status.State = "Started"
}

func (t *Trace) Stop(trace *gadgetv1alpha1.Trace) {
	if !t.started {
		trace.Status.OperationError = "Not started"
		return
	}

	err := t.stop()
	if err != nil {
		trace.Status.OperationWarning = err.Error()
	}

	trace.Status.State = "Stopped"
}

func (t *Trace) stop() error {
	t.tracer.Stop()
	t.tracer = nil
	t.started = false

	return bpfstats.DisableBPFStats()
}