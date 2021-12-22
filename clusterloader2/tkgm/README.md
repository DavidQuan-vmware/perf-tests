# Method to run clusterloader2 on clusters deployed by tanzu in vsphere platform

## Deploy a workload cluster by tanzu
1. need to edit vsphere ytt file(.tanzu/tkg/providers/infrastructure-vsphere/v0.7.7/ytt/base-template.yaml) to
   a. add `` kube-api-qps: "100" kube-api-burst: "100" `` for kubelet, kube-scheduler and kube-controller
   b. bind `` bind-address: 0.0.0.0`` to kube-scheduler and kube-controller
2. to add ``max-pods: "254"`` to kubelet, if want to test 250 pods per node
3. then create a workload cluster for testing


## Sync the code
1. git clone https://github.com/luwang-vmware/perf-tests.git
2. git checkout -b tkr_1.22_fips origin/tkr_1.22_fips
2. make softlink ``ln -s <dest_folder_of_perf_test_source_code> /src/k8s.io/perf-test``

## Sync the time of cluster with the hosting ESXi
1. check the set_time_align_esx.sh and update the vc ip and datacenter per your test env
2. ``bash set_time_align_esx.sh <your cluster name>``

## Retrieve etcd related crt from one of the controlplane
1. ``bash retrieve_crt.sh <one of controlplane ip>``

## Update the env var and export them
1. edit export_env.rc with the correct controlplane ip address
2. ``source export_env.rc``

## Run clusterloader2
1. cd /src/k8s.io/perf-tests; ./run-e2e.sh   --testconfig=./testing/node-throughput/config.yaml  --report-dir=/tmp/1  --masterip=$masterip --master-internal-ip=$masterip --enable-prometheus-server=true --tear-down-prometheus-server=false --prometheus-scrape-etcd=true --prometheus-scrape-kube-proxy=false  --prometheus-scrape-node-exporter=false  --prometheus-manifest-path /src/k8s.io//perf-tests/clusterloader2/pkg/prometheus/manifests/ --alsologtostderr --provider vsphere  2>&1


## Do prometheus snapshot
1. enable snapshot using ``kubectl  patch prometheus k8s -n monitoring --type merge --patch '{"spec":{"enableAdminAPI":true}}'``
2. get prometheus pod ip using ``kubectl get pods -n monitoring -o wide``
3. create a pod ``create curl pod kubectl run curl --image harbor-repo.vmware.com/relengfortkgi/appropriate/curl --restart=Never  -- sleep 36000``
4. issue a snapshot operation`` kubectl exec -it curl -- curl -XPOST  http://100.96.35.115:9090/api/v1/admin/tsdb/snapshot?skip_head=true``
{"status":"success","data":{"name":"20210719T090905Z-13e016af8a51d965"}}
5. copy the snapshot from the prometheus pod to the outside `` kubectl cp -n monitoring prometheus-k8s-0:/prometheus/snapshots/20210719T090905Z-13e016af8a51d965/ -c prometheus ./ ``
6. 