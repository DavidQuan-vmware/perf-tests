/*
Copyright 2019 The Kubernetes Authors.

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
	"context"
	"fmt"
	"time"
	"sync"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/perf-tests/clusterloader2/pkg/errors"
	"k8s.io/perf-tests/clusterloader2/pkg/execservice"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement"
	measurementutil "k8s.io/perf-tests/clusterloader2/pkg/measurement/util"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/checker"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/informer"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/runtimeobjects"
	"k8s.io/perf-tests/clusterloader2/pkg/measurement/util/workerqueue"
	"k8s.io/perf-tests/clusterloader2/pkg/util"
)

const (
	serviceCreationLatencyName           = "ServiceCreationLatency"
	serviceCreationLatencyWorkers        = 10
	defaultServiceCreationLatencyTimeout = 10 * time.Minute
	defaultCheckInterval                 = 10 * time.Second
	pingBackoff                          = 1 * time.Second
	pingChecks                           = 10
	// backendThreshold used to check whether total backend number curled from the services is
	// larger than/equal to this value.
	// 0 means that the testing disabled this check.
	// regexString is the regex expression string, which is used to filter the main caring info from the service curl output
	// regexString define depends on the container image used by services' pod.
	// backendThreshold need to be enabled with regexString, align with the container image.
	// example is in testing/svc/
	backendThreshold = 0
	regexString      = ""

	creatingPhase     = "creating"
	ipAssigningPhase  = "ipAssigning"
	reachabilityPhase = "reachability"
	startCheckingPhase = "startChecking"
	consecutiveSuccCheckStartPhase = "consecutiveSuccCheckStart"
)

func init() {
	if err := measurement.Register(serviceCreationLatencyName, createServiceCreationLatencyMeasurement); err != nil {
		klog.Fatalf("cant register service %v", err)
	}
}

func createServiceCreationLatencyMeasurement() measurement.Measurement {
	return &serviceCreationLatencyMeasurement{
		selector:      measurementutil.NewObjectSelector(),
		//queue:         workerqueue.NewWorkerQueue(serviceCreationLatencyWorkers),
		creationTimes: measurementutil.NewObjectTransitionTimes(serviceCreationLatencyName),
		svcBackends: measurementutil.NewSvcBackends(serviceCreationLatencyName),
		pingCheckers:  checker.NewMap(),
	}
}

type serviceCreationLatencyMeasurement struct {
	selector         *measurementutil.ObjectSelector
	waitTimeout      time.Duration
	stopCh           chan struct{}
	isRunning        bool
	queue            workerqueue.Interface
	client           clientset.Interface
	creationTimes    *measurementutil.ObjectTransitionTimes
	svcBackends      *measurementutil.SvcBackends
	pingCheckers     checker.Map
	lock             sync.Mutex
	succeedCheckNums int
	checkerWorkers   int
	backendThreshold int
	regexString      string
	regexpObj        *regexp.Regexp
}

// Execute executes service startup latency measurement actions.
// Services can be specified by field and/or label selectors.
// If namespace is not passed by parameter, all-namespace scope is assumed.
// "start" action starts observation of the services.
// "waitForReady" waits until all services are reachable.
// "gather" returns service created latency summary.
// This measurement only works for services with ClusterIP, NodePort and LoadBalancer type.
func (s *serviceCreationLatencyMeasurement) Execute(config *measurement.Config) ([]measurement.Summary, error) {
	s.client = config.ClusterFramework.GetClientSets().GetClient()
	action, err := util.GetString(config.Params, "action")
	if err != nil {
		return nil, err
	}
	if !config.ClusterLoaderConfig.EnableExecService {
		return nil, fmt.Errorf("enable-exec-service flag not enabled")
	}

	switch action {
	case "start":
		if err := s.selector.Parse(config.Params); err != nil {
			return nil, err
		}
		s.waitTimeout, err = util.GetDurationOrDefault(config.Params, "waitTimeout", defaultServiceCreationLatencyTimeout)
		if err != nil {
			return nil, err
		}
		s.checkerWorkers, err = util.GetIntOrDefault(config.Params, "parallel_checker_workers", serviceCreationLatencyWorkers)
		if err != nil {
			return nil, err
		}
		s.queue = workerqueue.NewWorkerQueue(s.checkerWorkers)
		s.succeedCheckNums, err = util.GetIntOrDefault(config.Params, "consecutive_succeed_checks", pingChecks)
		if err != nil {
			return nil, err
		}
		s.backendThreshold, err = util.GetIntOrDefault(config.Params, "backendThreshold", backendThreshold)
		if err != nil {
			return nil, err
		}
		s.regexString, err = util.GetStringOrDefault(config.Params, "regexString", regexString)
		if err != nil {
			return nil, err
		}
		s.regexString = "Server address: ([0-9.]+):80" 
		if s.regexString != "" {
			s.regexpObj, err = regexp.Compile(s.regexString)
			if err != nil {
				return nil, err
			}
		} else {
			s.regexpObj = nil
		}

		return nil, s.start()
	case "waitForReady":
		return nil, s.waitForReady()
	case "gather":
		return s.gather(config.Identifier)
	default:
		return nil, fmt.Errorf("unknown action %v", action)
	}
}

// Dispose cleans up after the measurement.
func (s *serviceCreationLatencyMeasurement) Dispose() {
	if s.isRunning {
		s.isRunning = false
		close(s.stopCh)
	}
	s.queue.Stop()
	s.lock.Lock()
	defer s.lock.Unlock()
	s.pingCheckers.Dispose()
}

// String returns a string representation of the metric.
func (s *serviceCreationLatencyMeasurement) String() string {
	return serviceCreationLatencyName + ": " + s.selector.String()
}

func (s *serviceCreationLatencyMeasurement) start() error {
	if s.isRunning {
		klog.V(2).Infof("%s: service creation latency measurement already running", s)
		return nil
	}
	klog.V(2).Infof("%s: starting service creation latency measurement...", s)

	s.isRunning = true
	s.stopCh = make(chan struct{})

	i := informer.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				s.selector.ApplySelectors(&options)
				return s.client.CoreV1().Services(s.selector.Namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				s.selector.ApplySelectors(&options)
				return s.client.CoreV1().Services(s.selector.Namespace).Watch(context.TODO(), options)
			},
		},
		func(oldObj, newObj interface{}) {
			f := func() {
				s.handleObject(oldObj, newObj)
			}
			s.queue.Add(&f)
		},
	)
	return informer.StartAndSync(i, s.stopCh, informerSyncTimeout)
}

func (s *serviceCreationLatencyMeasurement) waitForReady() error {
	return wait.Poll(defaultCheckInterval, s.waitTimeout, func() (bool, error) {
		for _, svcType := range []corev1.ServiceType{corev1.ServiceTypeClusterIP, corev1.ServiceTypeNodePort, corev1.ServiceTypeLoadBalancer} {
			reachable := s.creationTimes.Count(phaseName(reachabilityPhase, svcType))
			created := s.creationTimes.Count(phaseName(creatingPhase, svcType))
			klog.V(2).Infof("%s type %s: %d created, %d reachable", s, svcType, created, reachable)
			if created != reachable {
				return false, nil
			}
		}
		return true, nil
	})
}

var serviceCreationTransitions = map[string]measurementutil.Transition{
	"create_to_available_clusterip": {
		From: phaseName(creatingPhase, corev1.ServiceTypeClusterIP),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeClusterIP),
	},
	"create_to_startchecking_clusterip": {
		From: phaseName(creatingPhase, corev1.ServiceTypeClusterIP),
		To:   phaseName(startCheckingPhase, corev1.ServiceTypeClusterIP),
	},
	"startchecking_to_consecutivestart_clusterip": {
		From: phaseName(startCheckingPhase, corev1.ServiceTypeClusterIP),
		To:   phaseName(consecutiveSuccCheckStartPhase, corev1.ServiceTypeClusterIP),
	},
	"startchecking_to_available_clusterip": {
		From: phaseName(startCheckingPhase, corev1.ServiceTypeClusterIP),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeClusterIP),
	},
	"consecutivestart_to_available_clusterip": {
		From: phaseName(consecutiveSuccCheckStartPhase, corev1.ServiceTypeClusterIP),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeClusterIP),
	},
	"create_to_available_nodeport": {
		From: phaseName(creatingPhase, corev1.ServiceTypeNodePort),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeNodePort),
	},
	"create_to_assigned_loadbalancer": {
		From: phaseName(creatingPhase, corev1.ServiceTypeLoadBalancer),
		To:   phaseName(ipAssigningPhase, corev1.ServiceTypeLoadBalancer),
	},
	"assigned_to_available_loadbalancer": {
		From: phaseName(ipAssigningPhase, corev1.ServiceTypeLoadBalancer),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeLoadBalancer),
	},
	"create_to_available_loadbalancer": {
		From: phaseName(creatingPhase, corev1.ServiceTypeLoadBalancer),
		To:   phaseName(reachabilityPhase, corev1.ServiceTypeLoadBalancer),
	},
}

func (s *serviceCreationLatencyMeasurement) gather(identifier string) ([]measurement.Summary, error) {
	var summaries []measurement.Summary

	klog.V(2).Infof("%s: gathering service created latency measurement...", s)
	if !s.isRunning {
		return nil, fmt.Errorf("metric %s has not been started", s)
	}

	// NOTE: For ClusterIP or NodePort type of service, the cluster ip or node port is assigned as part of service creation API call, so the ipAssigning phase is no sense.
	serviceCreationLatency := s.creationTimes.CalculateTransitionsLatency(serviceCreationTransitions, measurementutil.MatchAll)

	content, err := util.PrettyPrintJSON(measurementutil.LatencyMapToPerfData(serviceCreationLatency))
	if err != nil {
		return nil, err
	}
	summary := measurement.CreateSummary(fmt.Sprintf("%s_%s", serviceCreationLatencyName, identifier), "json", content)
	summaries = append(summaries, summary)

	if s.backendThreshold != 0 {
		svcBackendNum := s.svcBackends.CalculateBackendNum()
		content, err = util.PrettyPrintJSON(svcBackendNum)
		if err != nil {
			return nil, err
		}
		summary = measurement.CreateSummary(fmt.Sprintf("%s_%s_BackendNums", serviceCreationLatencyName, identifier), "json", content)
		summaries = append(summaries, summary)

		if len(svcBackendNum) == 0 {
			err = errors.NewMetricViolationError(
				"service creation latency",
				fmt.Sprintf("%s_%s can not get any backends following pattern %s", serviceCreationLatencyName, identifier, s.regexString))
		}
		for svc, num := range svcBackendNum {
			if num < s.backendThreshold {
				klog.Errorf("only found %d backends for svc %s, expected at least %d backends", num, svc, s.backendThreshold)
				err = errors.NewMetricViolationError(
					"service creation latency",
					fmt.Sprintf("some services can not get at least %d backends", s.backendThreshold))
			}
		}
	}
	return summaries, err
}

func (s *serviceCreationLatencyMeasurement) handleObject(oldObj, newObj interface{}) {
	var oldService *corev1.Service
	var newService *corev1.Service
	var ok bool
	oldService, ok = oldObj.(*corev1.Service)
	if oldObj != nil && !ok {
		klog.Errorf("%s: uncastable old object: %v", s, oldObj)
		return
	}
	newService, ok = newObj.(*corev1.Service)
	if newObj != nil && !ok {
		klog.Errorf("%s: uncastable new object: %v", s, newObj)
		return
	}
	if isEqual := oldService != nil &&
		newService != nil &&
		equality.Semantic.DeepEqual(oldService.Spec, newService.Spec) &&
		equality.Semantic.DeepEqual(oldService.Status, newService.Status); isEqual {
		return
	}

	// TODO(#680): Make it thread-safe.
	if !s.isRunning {
		return
	}
	if newObj == nil {
		if err := s.deleteObject(oldService); err != nil {
			klog.Errorf("%s: delete checker error: %v", s, err)
		}
		return
	}
	if err := s.updateObject(newService); err != nil {
		klog.Errorf("%s: create checker error: %v", s, err)
	}
}

func (s *serviceCreationLatencyMeasurement) deleteObject(svc *corev1.Service) error {
	key, err := runtimeobjects.CreateMetaNamespaceKey(svc)
	if err != nil {
		return fmt.Errorf("meta key created error: %v", err)
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	s.pingCheckers.DeleteAndStop(key)
	return nil
}

func (s *serviceCreationLatencyMeasurement) updateObject(svc *corev1.Service) error {
	// This measurement only works for services with ClusterIP, NodePort and LoadBalancer type.
	if svc.Spec.Type != corev1.ServiceTypeClusterIP && svc.Spec.Type != corev1.ServiceTypeNodePort && svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil
	}
	key, err := runtimeobjects.CreateMetaNamespaceKey(svc)
	if err != nil {
		return fmt.Errorf("meta key created error: %v", err)
	}
	if _, exists := s.creationTimes.Get(key, phaseName(creatingPhase, svc.Spec.Type)); !exists {
		s.creationTimes.Set(key, phaseName(creatingPhase, svc.Spec.Type), svc.CreationTimestamp.Time)
	}
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer && len(svc.Status.LoadBalancer.Ingress) < 1 {
		return nil
	}
	// NOTE: For ClusterIP or NodePort type of service, the cluster ip or node port is assigned as part of service creation API call, so the ipAssigning phase is no sense.
	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		if _, exists := s.creationTimes.Get(key, phaseName(ipAssigningPhase, svc.Spec.Type)); exists {
			return nil
		}
		s.creationTimes.Set(key, phaseName(ipAssigningPhase, svc.Spec.Type), time.Now())
	}
	pc := &pingChecker{
		callerName:       s.String(),
		svc:              svc,
		creationTimes:    s.creationTimes,
		svcBackends:      s.svcBackends,
		succeedCheckNums: s.succeedCheckNums,
		regexpObj:        s.regexpObj,
		stopCh:           make(chan struct{}),
	}
	pc.run()
	s.lock.Lock()
	defer s.lock.Unlock()
	s.pingCheckers.Add(key, pc)

	return nil
}

func phaseName(phase string, serviceType corev1.ServiceType) string {
	return fmt.Sprintf("%s_%s", phase, serviceType)
}

type pingChecker struct {
	callerName       string
	svc              *corev1.Service
	creationTimes    *measurementutil.ObjectTransitionTimes
	svcBackends      *measurementutil.SvcBackends
	stopCh           chan struct{}
	succeedCheckNums int
	regexpObj        *regexp.Regexp
}

func (p *pingChecker) run() {
	key, err := runtimeobjects.CreateMetaNamespaceKey(p.svc)
	if err != nil {
		klog.Errorf("%s: meta key created error: %v", p.callerName, err)
		return
	}
	success := 0
	for {
		select {
		case <-p.stopCh:
			return
		default:
			if _, exists := p.creationTimes.Get(key, phaseName(reachabilityPhase, p.svc.Spec.Type)); exists {
				return
			}
			// TODO(#685): Make ping checks less communication heavy.
			pod, err := execservice.GetPod()
			if err != nil {
				klog.Warningf("call to execservice.GetPod() ended with error: %v", err)
				success = 0
				time.Sleep(pingBackoff)
				continue
			}
			if _, exists := p.creationTimes.Get(key, phaseName(startCheckingPhase, p.svc.Spec.Type)); !exists {
				p.creationTimes.Set(key, phaseName(startCheckingPhase, p.svc.Spec.Type), time.Now())
			}
			if success == 0 {
				p.creationTimes.Set(key, phaseName(consecutiveSuccCheckStartPhase, p.svc.Spec.Type), time.Now())
			}
			msg := ""
			cmd := ""
			switch p.svc.Spec.Type {
			case corev1.ServiceTypeClusterIP:
				// curl parameter is https://www.mit.edu/afs.new/sipb/user/ssen/src/curl-7.11.1/docs/curl.html
				// we use 3 as the value of -m, instead of the default timeout value 120s, to make the service creation time more precise
				cmd = fmt.Sprintf("curl -m 3 -s -S %s:%d", p.svc.Spec.ClusterIP, p.svc.Spec.Ports[0].Port)
				msg, err = execservice.RunCommand(pod, cmd)
			case corev1.ServiceTypeNodePort:
				cmd = fmt.Sprintf("curl -m 3 -s -S %s:%d", pod.Status.HostIP, p.svc.Spec.Ports[0].NodePort)
				msg, err = execservice.RunCommand(pod, cmd)
			case corev1.ServiceTypeLoadBalancer:
				cmd = fmt.Sprintf("curl -m 3 -s -S %s:%d", p.svc.Status.LoadBalancer.Ingress[0].IP, p.svc.Spec.Ports[0].Port)
				msg, err = execservice.RunCommand(pod, cmd)
			}
			if err != nil {
				klog.V(2).Infof("cmd %v in pod %v is error: %v", cmd, pod.Name, msg)
				success = 0
				time.Sleep(pingBackoff)
				continue
			}
			if p.regexpObj != nil {
				ip := p.regexpObj.FindStringSubmatch(msg)
				// [luwang-vmware] will think a more generic method to filter the user expected value
				if len(ip) >= 2 {
					p.svcBackends.Set(p.svc.Spec.ClusterIP, ip[1])
				}
			}
			success++
			if success == p.succeedCheckNums {
				p.creationTimes.Set(key, phaseName(reachabilityPhase, p.svc.Spec.Type), time.Now())
				klog.V(2).Infof("%v succeed to check", key)
			}
		}
	}
}

func (p *pingChecker) Stop() {
	close(p.stopCh)
}
