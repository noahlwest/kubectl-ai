#!/bin/bash

set -eou pipefail

# Positional args
# 1. ITERATIONS:  Number of times to run the loop (default: 3)
# 2. PROVIDER:    The LLM provider to use (default: openai)
# 3. MODEL:       The specific model to test (default: "Qwen/Qwen3-Next-80B-A3B-Instruct")
# 4. API_BASE:    The API base URL (default: "http://localhost:8000/v1")
# 5. CONCURRENCY: The amount of eval tasks to run in parallel (default: 5)
# Example usage: ./run-eval-loop.sh 5 openai Qwen/Qwen3-Next-80B-A3B-Instruct http://localhost:8000/v1
ITERATIONS=${1:-3}
PROVIDER=${2:-openai}
MODEL=${3:-"Qwen/Qwen3-Next-80B-A3B-Instruct"}
API_BASE=${4:-"http://localhost:8000/v1"}
CONCURRENCY=${5:-5}

# Check for required commands
for cmd in git go make; do
  if ! command -v $cmd &> /dev/null; then
    echo "Error: Required command '$cmd' is not installed. Aborting." >&2
    exit 1
  fi
done

# Build kubectl-ai and k8s-bench binaries
REPO_ROOT="$(git rev-parse --show-toplevel)"
cd ${REPO_ROOT}

BINDIR="${REPO_ROOT}/.build/bin"
mkdir -p "${BINDIR}"

cd "${REPO_ROOT}/cmd"
go build -o "${BINDIR}/kubectl-ai" .

cd "${REPO_ROOT}/k8s-bench"
go build -o "${BINDIR}/k8s-bench" .
K8S_BENCH_BIN="${BINDIR}/k8s-bench"

# Go back to REPO_ROOT to start running evals
cd "${REPO_ROOT}"

# Start evaluation loop
echo "Starting evaluation loop..."
echo "Runs:      $ITERATIONS"
echo "Provider:  $PROVIDER"
echo "Model:     $MODEL"
echo "API Base:  $API_BASE"

# Loop from 1 to the specified number of iterations
for i in $(seq 1 $ITERATIONS)
do
  # Create the unique directory name for this run
  OUTPUT_DIR="${REPO_ROOT}/.build/k8s-bench-${MODEL}-${i}"
  
  echo "**********"
  echo "loop_evals: Running iteration $i of $ITERATIONS..."
  echo "writing results to $OUTPUT_DIR"
  echo "**********"

  # Construct the arguments for the make command
  TEST_ARGS="--enable-tool-use-shim=false --llm-provider=${PROVIDER} --models=${MODEL} --quiet --output-dir=${OUTPUT_DIR} --create-kind-cluster --concurrency ${CONCURRENCY}"

  # Execute the make command and capture the evaluation time line
  run_time_line=$( \
    OPENAI_API_KEY="not needed" \
    OPENAI_API_BASE="$API_BASE" \
    TEST_ARGS="$TEST_ARGS" \
    make run-evals | tee /dev/tty | grep '^Total evaluation time:' \
  )

  # Check for errors in the make command
  if [ ${PIPESTATUS[0]} -ne 0 ]; then
    echo "Error on iteration $i during 'make run-evals'. Aborting loop."
    exit 1
  fi

  echo "---"
  echo "Analyzing results for iteration $i..."
  
  # Paths for analysis files
  MARKDOWN_FILE="${OUTPUT_DIR}/k8s-bench.md"
  JSON_FILE="${OUTPUT_DIR}/k8s-bench.js"

  # Run for markdown format
  "${K8S_BENCH_BIN}" analyze --input-dir="${OUTPUT_DIR}" --results-filepath="${MARKDOWN_FILE}" --output-format=markdown --show-failures
  if [ $? -ne 0 ]; then
    echo "Error on iteration $i during Markdown analysis. Aborting loop."
    exit 1
  fi

  # Run for json format
  "${K8S_BENCH_BIN}" analyze --input-dir="${OUTPUT_DIR}" --results-filepath="${JSON_FILE}" --output-format=json --show-failures
  if [ $? -ne 0 ]; then
    echo "Error on iteration $i during JSON analysis. Aborting loop."
    exit 1
  fi

  # Extract the time value and append it to the markdown file
  if [ -n "$run_time_line" ]; then
    time_value=$(echo $run_time_line | awk '{print $4}')
    
    # Append the time to the markdown file with some formatting
    echo "" >> "${MARKDOWN_FILE}"
    echo "---" >> "${MARKDOWN_FILE}"
    echo "**Total evaluation time:** ${time_value}" >> "${MARKDOWN_FILE}"
    
    echo "Appended evaluation time to ${MARKDOWN_FILE}"
  else
    echo "Warning: Could not find evaluation time for iteration $i."
  fi

  echo "Finished iteration $i."
done

echo "All $ITERATIONS runs completed successfully!"