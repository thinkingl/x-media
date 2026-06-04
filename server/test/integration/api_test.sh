#!/bin/bash
set -e

API_URL="http://localhost:8080/api/v1"
SERVER_PID=""
TEST_MP4="$(cd "$(dirname "$0")/../fixtures" && pwd)/test.mp4"
TEST_H265="$(cd "$(dirname "$0")/../fixtures" && pwd)/h265_test.mp4"
PASS=0
FAIL=0
TEST_PORT=$((18000 + RANDOM % 1000))

log_info() { echo -e "\033[32m[INFO]\033[0m $1"; }
log_error() { echo -e "\033[31m[FAIL]\033[0m $1"; }
log_pass() { echo -e "\033[32m[PASS]\033[0m $1"; PASS=$((PASS+1)); }
log_fail() { echo -e "\033[31m[FAIL]\033[0m $1"; FAIL=$((FAIL+1)); }

assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        log_pass "$desc"
    else
        log_fail "$desc (expected='$expected', actual='$actual')"
    fi
}

assert_not_empty() {
    local desc="$1" value="$2"
    if [ -n "$value" ]; then
        log_pass "$desc"
    else
        log_fail "$desc (value is empty)"
    fi
}

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        log_info "Stopping server (PID=$SERVER_PID)..."
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

start_server() {
    log_info "Building server..."
    cd "$(dirname "$0")/../.."
    CGO_ENABLED=1 go build -o /tmp/x-media-test-server ./cmd/main.go

    log_info "Starting server on port $TEST_PORT..."
    /tmp/x-media-test-server -c /dev/stdin <<EOF &
server:
  http_addr: ":$TEST_PORT"
database:
  driver: "sqlite"
  dsn: "/tmp/x-media-test-$(date +%s).db"
log:
  level: "error"
EOF
    SERVER_PID=$!
    export API_URL="http://localhost:$TEST_PORT/api/v1"

    log_info "Waiting for server (PID=$SERVER_PID)..."
    for i in $(seq 1 20); do
        if curl -s "$API_URL/stats" > /dev/null 2>&1; then
            log_info "Server ready"
            return 0
        fi
        sleep 0.5
    done
    log_error "Server failed to start"
    exit 1
}

test_inputs() {
    log_info "=== Testing Inputs ==="

    # Create H264 file input
    local resp
    resp=$(curl -s -X POST "$API_URL/inputs" \
        -H "Content-Type: application/json" \
        -d "{
            \"name\": \"test-h264\",
            \"type\": \"file\",
            \"config\": \"{\\\"path\\\":\\\"$TEST_MP4\\\",\\\"loop\\\":false}\"
        }")
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create H264 input returns 201" "201" "$code"
    INPUT_H264_ID=$(echo "$resp" | jq -r '.data.id')
    assert_not_empty "H264 input has ID" "$INPUT_H264_ID"

    # Create H265 file input
    resp=$(curl -s -X POST "$API_URL/inputs" \
        -H "Content-Type: application/json" \
        -d "{
            \"name\": \"test-h265\",
            \"type\": \"file\",
            \"config\": \"{\\\"path\\\":\\\"$TEST_H265\\\",\\\"loop\\\":true}\"
        }")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create H265 input returns 201" "201" "$code"
    INPUT_H265_ID=$(echo "$resp" | jq -r '.data.id')
    assert_not_empty "H265 input has ID" "$INPUT_H265_ID"

    # List inputs
    resp=$(curl -s "$API_URL/inputs")
    local count=$(echo "$resp" | jq -r '.data | length')
    assert_eq "List inputs returns 2" "2" "$count"

    # Start H264 input
    resp=$(curl -s -X POST "$API_URL/inputs/$INPUT_H264_ID/start")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Start H264 input returns 200" "200" "$code"

    # Check status
    resp=$(curl -s "$API_URL/inputs/$INPUT_H264_ID")
    local status=$(echo "$resp" | jq -r '.data.status')
    assert_eq "H264 input status is running" "running" "$status"

    # Stop H264 input
    resp=$(curl -s -X POST "$API_URL/inputs/$INPUT_H264_ID/stop")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Stop H264 input returns 200" "200" "$code"

    # Verify stopped
    resp=$(curl -s "$API_URL/inputs/$INPUT_H264_ID")
    status=$(echo "$resp" | jq -r '.data.status')
    assert_eq "H264 input status is stopped" "stopped" "$status"
}

test_outputs() {
    log_info "=== Testing Outputs ==="

    # Create RTMP output
    local resp
    resp=$(curl -s -X POST "$API_URL/outputs" \
        -H "Content-Type: application/json" \
        -d '{
            "name": "test-rtmp",
            "type": "rtmp",
            "config": "{\"url\":\"rtmp://localhost/live/test\"}"
        }')
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create RTMP output returns 201" "201" "$code"
    OUTPUT_RTMP_ID=$(echo "$resp" | jq -r '.data.id')
    assert_not_empty "RTMP output has ID" "$OUTPUT_RTMP_ID"

    # Create RTSP output (server mode)
    resp=$(curl -s -X POST "$API_URL/outputs" \
        -H "Content-Type: application/json" \
        -d '{
            "name": "test-rtsp",
            "type": "rtsp",
            "config": "{\"mode\":\"server\",\"addr\":\":15544\"}"
        }')
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create RTSP output returns 201" "201" "$code"
    OUTPUT_RTSP_ID=$(echo "$resp" | jq -r '.data.id')

    # Create HTTP-FLV output
    resp=$(curl -s -X POST "$API_URL/outputs" \
        -H "Content-Type: application/json" \
        -d '{
            "name": "test-httpflv",
            "type": "http-flv",
            "config": "{\"addr\":\":18081\"}"
        }')
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create HTTP-FLV output returns 201" "201" "$code"
    OUTPUT_HTTPFLV_ID=$(echo "$resp" | jq -r '.data.id')

    # List outputs
    resp=$(curl -s "$API_URL/outputs")
    local count=$(echo "$resp" | jq -r '.data | length')
    assert_eq "List outputs returns 3" "3" "$count"

    # Start RTMP output
    resp=$(curl -s -X POST "$API_URL/outputs/$OUTPUT_RTMP_ID/start")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Start RTMP output returns 200" "200" "$code"

    # Stop RTMP output
    resp=$(curl -s -X POST "$API_URL/outputs/$OUTPUT_RTMP_ID/stop")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Stop RTMP output returns 200" "200" "$code"
}

test_pipes() {
    log_info "=== Testing Pipes ==="

    # Start input and output first
    curl -s -X POST "$API_URL/inputs/$INPUT_H265_ID/start" > /dev/null
    curl -s -X POST "$API_URL/outputs/$OUTPUT_RTMP_ID/start" > /dev/null

    # Create pipe
    local resp
    resp=$(curl -s -X POST "$API_URL/pipes" \
        -H "Content-Type: application/json" \
        -d "{
            \"input_id\": \"$INPUT_H265_ID\",
            \"output_id\": \"$OUTPUT_RTMP_ID\"
        }")
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Create pipe returns 201" "201" "$code"
    PIPE_ID=$(echo "$resp" | jq -r '.data.id')
    assert_not_empty "Pipe has ID" "$PIPE_ID"

    # Start pipe
    resp=$(curl -s -X POST "$API_URL/pipes/$PIPE_ID/start")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Start pipe returns 200" "200" "$code"

    # Check pipe status
    resp=$(curl -s "$API_URL/pipes/$PIPE_ID")
    local status=$(echo "$resp" | jq -r '.data.status')
    assert_eq "Pipe status is running" "running" "$status"

    # Duplicate pipe should fail
    resp=$(curl -s -X POST "$API_URL/pipes" \
        -H "Content-Type: application/json" \
        -d "{
            \"input_id\": \"$INPUT_H265_ID\",
            \"output_id\": \"$OUTPUT_RTMP_ID\"
        }")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Duplicate pipe returns 400" "400" "$code"

    # Stop pipe
    resp=$(curl -s -X POST "$API_URL/pipes/$PIPE_ID/stop")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Stop pipe returns 200" "200" "$code"

    # Stop input/output
    curl -s -X POST "$API_URL/inputs/$INPUT_H265_ID/stop" > /dev/null
    curl -s -X POST "$API_URL/outputs/$OUTPUT_RTMP_ID/stop" > /dev/null
}

test_stats() {
    log_info "=== Testing Stats ==="

    local resp
    resp=$(curl -s "$API_URL/stats")
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Get stats returns 200" "200" "$code"

    local active_inputs=$(echo "$resp" | jq -r '.data.active_inputs')
    assert_eq "Active inputs is 0 (all stopped)" "0" "$active_inputs"
}

test_logs() {
    log_info "=== Testing Logs ==="

    # Get logs
    local resp
    resp=$(curl -s "$API_URL/logs?lines=10")
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Get logs returns 200" "200" "$code"

    # Get log config
    resp=$(curl -s "$API_URL/logs/config")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Get log config returns 200" "200" "$code"
    local level=$(echo "$resp" | jq -r '.data.level')
    assert_eq "Log level is error" "error" "$level"

    # Update log config
    resp=$(curl -s -X PUT "$API_URL/logs/config" \
        -H "Content-Type: application/json" \
        -d '{"level":"info","max_size":50}')
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Update log config returns 200" "200" "$code"
}

test_delete_running() {
    log_info "=== Test delete protections ==="

    # Start an input
    curl -s -X POST "$API_URL/inputs/$INPUT_H265_ID/start" > /dev/null

    # Try to delete running input - should fail
    local resp
    resp=$(curl -s -X DELETE "$API_URL/inputs/$INPUT_H265_ID")
    local code=$(echo "$resp" | jq -r '.code')
    assert_eq "Delete running input returns 400" "400" "$code"

    # Stop and delete
    curl -s -X POST "$API_URL/inputs/$INPUT_H265_ID/stop" > /dev/null
    resp=$(curl -s -X DELETE "$API_URL/inputs/$INPUT_H265_ID")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Delete stopped input returns 200" "200" "$code"

    # Verify deleted
    resp=$(curl -s "$API_URL/inputs/$INPUT_H265_ID")
    code=$(echo "$resp" | jq -r '.code')
    assert_eq "Deleted input returns 404" "404" "$code"
}

test_cleanup() {
    log_info "=== Cleanup ==="

    curl -s -X DELETE "$API_URL/inputs/$INPUT_H264_ID" 2>/dev/null || true
    curl -s -X DELETE "$API_URL/outputs/$OUTPUT_RTMP_ID" 2>/dev/null || true
    curl -s -X DELETE "$API_URL/outputs/$OUTPUT_RTSP_ID" 2>/dev/null || true
    curl -s -X DELETE "$API_URL/outputs/$OUTPUT_HTTPFLV_ID" 2>/dev/null || true
}

# Main
start_server
test_inputs
test_outputs
test_pipes
test_stats
test_logs
test_delete_running
test_cleanup

echo ""
echo "=============================="
echo -e "Results: \033[32m${PASS} passed\033[0m, \033[31m${FAIL} failed\033[0m"
echo "=============================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
