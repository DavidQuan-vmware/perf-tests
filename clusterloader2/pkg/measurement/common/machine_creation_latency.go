/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import (

	"fmt"
	"time"
	"sync"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	//"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog"

	"k8s.io/perf-tests/clusterloader2/pkg/framework"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
	measurementutil "k8s.io/perf-tests/clusterloader2/pkg/measurement/util"
	//"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/checker"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/informer"
	//"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/runtimeobjects"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/workerqueue"
	"k8s.io/perf-tests/clusterloader2/pkg/util"

	capi "sigs.k8s.io/cluster-api/api/v1beta1"
)


const (
	machineCreationLatencyMeasurementName  = "MachineCreationLatency"
	m_defaultOperationTimeout           = 10 * time.Minute

	m_creatingPhase     = "creation"
//	m_startPhase = "startCreation"
	m_bootstrapreadyPhase = "BootstrapReady"
	m_infrareadyPhase  = "InfrastructureReady"
	m_nodehealthPhase = "NodeHealthy"
	m_healthCheckSucceedPhase = "HealthCheckSucceeded"
)


func init() {
	if err := measurement.Register(machineCreationLatencyMeasurementName, createMachineCreationLatencyMeasurement); err != nil {
		klog.Fatalf("cant register service %v", err)
	}
}

func createMachineCreationLatencyMeasurement() measurement.Measurement {
	return &machineCreationLatencyMeasurement{
			selector:   measurementutil.NewObjectSelector(),
			creationTimes: measurementutil.NewObjectTransitionTimes(machineCreationLatencyMeasurementName),
			machineRecords: make(map[string]*machineRecord),
			queue:      workerqueue.NewWorkerQueue(1),
		}
}

type machineRecord struct{
	name string
	machine_type string
	nodeReadyChanged bool
	deleted bool
	createdSucceed bool
}
func newMachineRecord(name string, machine_type string) *machineRecord {
	return &machineRecord{
		name: name,
		machine_type: machine_type,
		nodeReadyChanged: false,
		deleted: false,
		createdSucceed: false,
	}
}

type machineCreationLatencyMeasurement struct {
	apiVersion       string //mandatory
	kind             string //mandatory
	selector         *measurementutil.ObjectSelector //mandatory
	operationTimeout time.Duration   //not sure
	// countErrorMargin orders measurement to wait for number of pods to be in
	// <desired count - countErrorMargin, desired count> range
	// When using preemptibles on large scale, number of ready nodes is not stable
	// and reaching DesiredPodCount could take a very long time.
	countErrorMargin      int
	stopCh                chan struct{} //mandatory
	isRunning             bool //mandatory
	lock                  sync.Mutex
	queue                 workerqueue.Interface

	opResourceVersion     uint64
	gvr                   schema.GroupVersionResource
	clusterFramework      *framework.Framework  //must

	creationTimes *measurementutil.ObjectTransitionTimes  //mandatory
	machineRecords map[string]*machineRecord //mandatory
	expected_machine_num int
}

// Execute supports two actions:
// - start - Starts to observe machine CR
// - gather - Gathers and prints machine creation latency
// Does NOT support concurrency. Multiple calls to this measurement
// shouldn't be done within one step.
func (m *machineCreationLatencyMeasurement) Execute(config *measurement.Config) ([]measurement.Summary, error) {
	m.clusterFramework = config.ClusterFramework
	action, err := util.GetString(config.Params, "action")
	if err != nil {
		return nil, err
	}
	switch action {
	case "start":
		m.apiVersion, err = util.GetStringOrDefault(config.Params, "apiVersion", "cluster.x-k8s.io/v1alpha3")
		if err != nil {
			return nil, err
		}

		m.kind = "Machine"

		m.expected_machine_num, err = util.GetIntOrDefault(config.Params, "expected_machine_numbers", 0)
		if err != nil {
			return nil, err
		}
		if err = m.selector.Parse(config.Params); err != nil {
			return nil, err
		}
		m.operationTimeout, err = util.GetDurationOrDefault(config.Params, "operationTimeout", m_defaultOperationTimeout)
		if err != nil {
			return nil, err
		}

		//需要传入total cluster的个数及每个cluster的size
		return nil, m.start()
	case "waitForReady":
		return nil, m.waitForReady()
	case "gather":
		return m.gather(config.Identifier)
	default:
		return nil, fmt.Errorf("unknown action %v", action)
	}

}

// Dispose cleans up after the measurement.
func (m *machineCreationLatencyMeasurement) Dispose() {
	m.stop()
}

func (m *machineCreationLatencyMeasurement) stop() {
	a := 1
	a = a + 1
}
// String returns string representation of this measurement.
func (m *machineCreationLatencyMeasurement) String() string {
	return machineCreationLatencyMeasurementName + ": " + m.selector.String()
}

func (m *machineCreationLatencyMeasurement) start() error {
	if m.isRunning {
		klog.V(2).Infof("%v: wait for machine creation latency measurements already running", m)
		return nil
	}
	klog.V(2).Infof("%v: starting wait for machine creation latency measurements...", m)

	gv, err := schema.ParseGroupVersion(m.apiVersion)
	if err != nil {
		return err
	}
	gvk := gv.WithKind(m.kind)
	m.gvr, _ = meta.UnsafeGuessKindToResource(gvk)

	m.isRunning = true
	m.stopCh = make(chan struct{})
	i := informer.NewDynamicInformer(
		m.clusterFramework.GetDynamicClients().GetClient(),
		m.gvr,
		m.selector,
		func(odlObj, newObj interface{}) {
			//m.handleObject(odlObj, newObj)
			f := func() {
			 	m.handleObject(odlObj, newObj)
			}
			m.queue.Add(&f)
		},
	)
	return informer.StartAndSync(i, m.stopCh, informerSyncTimeout)
}

func (m *machineCreationLatencyMeasurement) waitForReady() error {
	return wait.Poll(defaultCheckInterval, m.operationTimeout, func() (bool, error) {
		total_machines_num := 0
		for _, mRecord := range m.machineRecords {
			if mRecord.deleted == false  && mRecord.createdSucceed == true {
				total_machines_num = total_machines_num + 1
			}
		}
		klog.V(2).Infof("%v   %v", total_machines_num, m.expected_machine_num)
		if total_machines_num != m.expected_machine_num {
            return false, nil
		}
		return true, nil
	})
}


// handleObject manages checker for given controlling machine object.
// This function does not return errors only logs them. All possible errors will be caught in gather function.
// If this function does not executes correctly, verifying number of machine will fail,
// causing incorrect objects number error to be returned.
func (m *machineCreationLatencyMeasurement) handleObject(oldObj, newObj interface{}) {
	var oldRuntimeObj runtime.Object
	var newRuntimeObj runtime.Object
	var ok bool
	oldRuntimeObj, ok = oldObj.(runtime.Object)
	if oldObj != nil && !ok {
		klog.Errorf("%s: uncastable old object: %v", m, oldObj)
		return
	}
	newRuntimeObj, ok = newObj.(runtime.Object)
	if newObj != nil && !ok {
		klog.Errorf("%s: uncastable new object: %v", m, newObj)
		return
	}

	// Acquire the lock before defining defered function to ensure it
	// will be called under the same lock.
	m.lock.Lock()
	defer m.lock.Unlock()

	// defer func() {
	// 	if err := m.updateCacheLocked(oldRuntimeObj, newRuntimeObj); err != nil {
	// 		klog.Errorf("%s: error when updating cache: %v", w, err)
	// 	}
	// }()

	// isEqual, err := runtimeobjects.IsEqualRuntimeObjectsSpec(oldRuntimeObj, newRuntimeObj)
	// if err != nil {
	// 	klog.Errorf("%s: comparing specs error: %v", m, err)
	// 	return
	// }
	// if isEqual {
	// 	// Skip updates without changes in the spec.
	// 	return
	// }

	if !m.isRunning {
		return
	}

	if newRuntimeObj == nil {
		if err := m.deleteObject(oldRuntimeObj); err != nil {
	        klog.Errorf("%s: delete checker error: %v", m, err)
		}
	} else {
		if err := m.addUpdateObject(newRuntimeObj); err != nil {
			klog.Errorf("%s: create checker error: %v", m, err)
		}
	}
}

var machineStatusTransition = map[string]measurementutil.Transition{
	"create_to_bootstrapready_md": {
		From: phaseName(m_creatingPhase, "md"),
		To:   phaseName(m_bootstrapreadyPhase, "md"),
	},
	"bootstrapready_to_infraready_md": {
		From: phaseName(m_bootstrapreadyPhase, "md"),
		To:   phaseName(m_infrareadyPhase, "md"),
	},
	"infraready_to_nodeready_md": {
		From: phaseName(m_infrareadyPhase, "md"),
		To:   phaseName(m_nodehealthPhase, "md"),
	},
	"create_to_bootstrapready_cp": {
		From: phaseName(m_creatingPhase, "cp"),
		To:   phaseName(m_bootstrapreadyPhase, "cp"),
	},
	"bootstrapready_to_infraready_cp": {
		From: phaseName(m_bootstrapreadyPhase, "cp"),
		To:   phaseName(m_infrareadyPhase, "cp"),
	},
	"infraready_to_nodeready_cp": {
		From: phaseName(m_infrareadyPhase, "cp"),
		To:   phaseName(m_nodehealthPhase, "cp"),
	},
}

func (m *machineCreationLatencyMeasurement) gather(identifier string) ([]measurement.Summary, error) {
	klog.V(2).Infof("%s: gathering pod startup latency measurement...", m)
	if !m.isRunning {
		return nil, fmt.Errorf("metric %s has not been started")
	}
	// for n, _ := range m.machineRecords {
	// 	klog.V(2).Infof(n)
	// 	//klog.V(2).Infof(ma.deleted)
	// }
    m.creationTimes.Print()

	total_machines_num := len(m.machineRecords)
	deleted_num := 0
	for _, mRecord := range m.machineRecords {
		if mRecord.deleted {
			deleted_num = deleted_num + 1
		}
		klog.V(2).Infof("machine %s:  createdSucceed is %v, nodeReadyChanged is %v, Deleted is %v", mRecord.name, mRecord.createdSucceed,  mRecord.nodeReadyChanged, mRecord.deleted)
	}
	klog.V(2).Infof("total get machine number is %d, deleted machine number is %d", total_machines_num, deleted_num)



	machineCreationLatency := m.creationTimes.CalculateTransitionsLatency(machineStatusTransition, measurementutil.MatchAll)

	content, err := util.PrettyPrintJSON(measurementutil.LatencyMapToPerfData(machineCreationLatency))
	if err != nil {
		return nil, err
	}
	summary := measurement.CreateSummary(fmt.Sprintf("%s_%s", machineCreationLatencyMeasurementName, identifier), "json", content)
	return []measurement.Summary{summary}, nil
}

func (m *machineCreationLatencyMeasurement) deleteObject(obj runtime.Object) error {
	klog.V(2).Infof("%v: get deleted obj...", m)
	if obj == nil {
		return nil
	}
    machine, err := convertedToMachine(obj)
	if err != nil {
		return err
	}
	key, err := createMetaNamespaceKey(machine)
	m.machineRecords[key].deleted = true
	klog.V(2).Infof(key)
	return nil
}

func (m *machineCreationLatencyMeasurement) addUpdateObject(obj runtime.Object) error {
	klog.V(2).Infof("%v: get new/updated obj...", m)
    machine, err := convertedToMachine(obj)
	if err != nil {
		return err
	}

	key, err := createMetaNamespaceKey(machine)
	machine_type := "cp"
	if strings.Contains(key, "md") {
		machine_type = "md"
	}
	if _, exists := m.machineRecords[key]; !exists {
		m.machineRecords[key] = newMachineRecord(key, machine_type)
	}

	if _, exists := m.creationTimes.Get(key, phaseMachine(m_creatingPhase, machine_type)); !exists {
		m.creationTimes.Set(key, phaseMachine(m_creatingPhase, machine_type), machine.ObjectMeta.GetCreationTimestamp().Time)
	}

	klog.V(2).Infof(key)
	for _, condition := range machine.GetConditions() {
		
		klog.V(2).Infof("%v", condition)
		switch cond := condition.Type; cond {
		case m_bootstrapreadyPhase: {
			if condition.Status == "True" {
				m.creationTimes.Set(key, phaseMachine(m_bootstrapreadyPhase, machine_type), condition.LastTransitionTime.Time)
			} 
		}
		case m_infrareadyPhase:
			if condition.Status == "True" {
				m.creationTimes.Set(key, phaseMachine(m_infrareadyPhase, machine_type), condition.LastTransitionTime.Time)
			} 
			// else {
			// 	if _, exists := m.creationTimes.Get(key, phaseMachine(m_startPhase, machine_type)); exists {
			// 		continue
			// 	}
			// 	if  strings.Contains(condition.Reason, "WaitingForInfrastructure") {
			// 		m.creationTimes.Set(key, phaseMachine(m_startPhase, machine_type), condition.LastTransitionTime.Time)
			// 	}
			// }
		case m_healthCheckSucceedPhase:
		case m_nodehealthPhase:
			if condition.Status == "True" {
				klog.V(2).Infof("-------------")
				// nodeready's lasttranistiontime may change, due to node is not ready, clusterapi will heal it to normal
				if ti, exists := m.creationTimes.Get(key, phaseMachine(m_nodehealthPhase, machine_type)); exists {
					if ti != condition.LastTransitionTime.Time {
						m.machineRecords[key].nodeReadyChanged = true
					} 
				}
				m.creationTimes.Set(key, phaseMachine(m_nodehealthPhase, machine_type), condition.LastTransitionTime.Time)
				m.machineRecords[key].createdSucceed = true
			} else {
				m.machineRecords[key].createdSucceed = false
			}
		}
	}
	return nil
}


func convertedToMachine(obj runtime.Object) (*capi.Machine, error) {
	machine := &capi.Machine{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.(*unstructured.Unstructured).UnstructuredContent(), machine)
	if err != nil {
		klog.V(2).Infof("Failed to convert object to Machine...")
		return machine, err
	}
	return machine, nil
}

func createMetaNamespaceKey(machine *capi.Machine) (string, error) {
    ns := machine.ObjectMeta.GetNamespace()
	cls_name := machine.Spec.ClusterName
	machine_name := machine.ObjectMeta.GetName()
	return ns + "/" + cls_name + "/" + machine_name, nil
}

 func phaseMachine(phase string, machine_type string) string {
 	return fmt.Sprintf("%s_%s", phase, machine_type)
 }