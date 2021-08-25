#!/usr/bin/env bash
# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Runner script for the benchmark tool that works when called from any path.

set -o errexit
set -o nounset
set -o pipefail

BENCHMARK_ARGS=${BENCHMARK_ARGS:-} # Parameters to configure the benchmark tool.
BENCHMARK_DIR="${GOPATH}/src/k8s.io/perf-tests/benchmark"

# Compile the tool.
cd ${BENCHMARK_DIR}
make benchmark
cd -

# Run the tool with args passed to this runner.
#${BENCHMARK_DIR}/build/benchmark --alsologtostderr ${BENCHMARK_ARGS} --left-job-name tkg-1.3.1-patch-ubuntu-node-throughput-k8s-1.20  --right-job-name fips-tkg-1.3.1-patch-ubuntu-node-throughput-k8s-1.20  --log-source-mode gcs_raw --run-selection-scheme last-n-runs  --n-runs-count 30 --comparison-scheme Avg-Test --gcs-credential-file /tmp/gcs.json
#${BENCHMARK_DIR}/build/benchmark --alsologtostderr ${BENCHMARK_ARGS} --left-job-name tkg-1.3.1-patch-ubuntu-node-throughput-k8s-1.20  --right-job-name fips-tkg-1.3.1-patch-ubuntu-node-throughput-k8s-1.20  --log-source-mode gcs_raw --run-selection-scheme last-n-runs  --n-runs-count 30 --comparison-scheme KS-Test --gcs-credential-file /tmp/gcs.json
#${BENCHMARK_DIR}/build/benchmark --alsologtostderr ${BENCHMARK_ARGS} --left-job-name tkg-1.3.1-patch-1c-100w-k8s-1.20 --right-job-name fips-tkg-1.3.1-patch-1c-100w-k8s-1.20  --log-source-mode gcs_raw --run-selection-scheme last-n-runs  --n-runs-count 10 --comparison-scheme Avg-Test --gcs-credential-file /tmp/gcs.json
#${BENCHMARK_DIR}/build/benchmark --alsologtostderr ${BENCHMARK_ARGS} --left-job-name tkg-1.3.1-patch-1c-100w-k8s-1.20 --right-job-name fips-tkg-1.3.1-patch-1c-100w-k8s-1.20  --log-source-mode gcs_raw --run-selection-scheme last-n-runs  --n-runs-count 30 --comparison-scheme KS-Test --gcs-credential-file /tmp/gcs.json
${BENCHMARK_DIR}/build/benchmark --alsologtostderr ${BENCHMARK_ARGS} --left-job-name tkg-1.3.1-patch-1c-500w-k8s-1.20 --right-job-name fips-tkg-1.3.1-patch-1c-500w-k8s-1.20  --log-source-mode gcs_raw --run-selection-scheme last-n-runs  --n-runs-count 10 --comparison-scheme Avg-Test --gcs-credential-file /tmp/gcs.json