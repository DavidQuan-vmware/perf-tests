# How to enable upstream perfdash to use in our internal testing

## prepare gcs bucket and gcs credentials
* created gcs bucket in https://console.cloud.google.com/storage/browser/tkg-scale (gs://tkg-scale)
* Reach out to me if you want to upload/review data in the bucket

## prepare jobs file which are as the paremeters when starting perfdash
* sample is located in http://10.199.17.201/reports/scale-jobs/jobs and http://10.199.17.201/reports/scale-jobs/1.20.yaml 
* 1.20.yaml concludes array of periodics. Each periodics must has name(which is mapping to the folder in gcs gs://tkg-scale/log/<periodics.name>). The sub-folder under the it are the different runs of the testing, the sub-folder name must be digital. Under each digital sub-folder, the detail output generating from the clusterloader2 must locate in ./artifacts. The tags.perfDashPrefix is the name showing in the Job Column of perfdash UI. The scenarios is just listing what kinds of test scenarios runs in such kinds of test cluster.

## how to start perfdash locally
1. git clone https://github.com/luwang-vmware/perf-tests.git
2. git checkout -b tkgm_master origin/tkgm_master
3. cd perfdash/; go build
4.  ./perfdash --www --address=0.0.0.0:8090 --builds=20 --force-builds --githubConfigDir=http://10.199.17.201/reports/scale-jobs/jobs --logsBucket tkg-scale --credentialPath ./gcs.json 
5. Open with http://<ip>:8090. Note that it might take a short while for perfdash to start since it needs to fetch the job artifacts first.
