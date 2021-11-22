# Method to run clusterloader2 on mgmt cluster for clusterapi testing by tanzu in vsphere platform

## Sync the code
1. git clone https://github.com/luwang-vmware/perf-tests.git
2. git checkout -b clusterapi origin/clusterapi
2. make softlink ``ln -s <dest_folder_of_perf_test_source_code> /src/k8s.io/perf-test``

## Update the storage class per your testing env
1. update pkg/prometheus/manifests/0ssd-storage-class.yaml

## Update the worker ip manually
1. edit file in pkg/prometheus/manifests/exporter/worker/worker-endpoints.yaml

## Sync the time of cluster with the hosting ESXi (no need to sync the code once tanzu support ntp)
1. check the set_time_align_esx.sh and update the vc ip and datacenter per your test env
2. ``bash set_time_align_esx.sh <your cluster name>``

## Retrieve etcd related crt from one of the controlplane
1. ``bash retrieve_crt.sh <one of controlplane ip>``

## Update the env var and export them
1. edit export_env.rc with the correct controlplane ip address
2. ``source export_env.rc``

## Manully apply the yaml under pkg/prometheus/manifests/exporter/
Will be automated in future, please manully do it for now
2. kubectl apply -f capi/
3. kubectl apply -f capv/
4. kubectl apply -f worker/
5. kubectl apply -f node_exporter_ds/

## Manually import grafana dashboard
Will be automated in future, please manully do it for now
1. import the clusterapi.json from pkg/prometheus/manifests/dashboard/
2. import the pod_container_resource_cadvisor.json from pkg/prometheus/manifests/dashboard/
3. import the cluster-host-resource-node-exporter.json from pkg/prometheus/manifests/dashboard/

## Run clusterloader2
1. cd /src/k8s.io/perf-tests/clusterloader2/; ./run-e2e.sh   --testconfig=./testing/clusterapi/config.yaml  --report-dir=/tmp/1  --masterip=$masterip --master-internal-ip=$masterip --enable-prometheus-server=true --tear-down-prometheus-server=false --prometheus-scrape-etcd=true --prometheus-scrape-kube-proxy=false  --prometheus-scrape-node-exporter=false --prometheus-scrape-kubelets=true  --prometheus-manifest-path /src/k8s.io//perf-tests/clusterloader2/pkg/prometheus/manifests/ --alsologtostderr --provider vsphere  2>&1

## Do prometheus snapshot
1. enable snapshot using ``kubectl  patch prometheus k8s -n monitoring --type merge --patch '{"spec":{"enableAdminAPI":true}}'``
2. get prometheus pod ip using ``kubectl get pods -n monitoring -o wide``
3. create a pod ``create curl pod kubectl run curl --image harbor-repo.vmware.com/relengfortkgi/appropriate/curl --restart=Never  -- sleep 36000``
4. issue a snapshot operation`` kubectl exec -it curl -- curl -XPOST  http://100.96.35.115:9090/api/v1/admin/tsdb/snapshot?skip_head=true``
{"status":"success","data":{"name":"20210719T090905Z-13e016af8a51d965"}}
5. copy the snapshot from the prometheus pod to the outside `` kubectl cp -n monitoring prometheus-k8s-0:/prometheus/snapshots/20210719T090905Z-13e016af8a51d965/ -c prometheus ./ ``
6. 