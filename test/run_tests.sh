#!/bin/bash
set -euo pipefail

API="http://localhost:18090/api/v1"
PASS=0
FAIL=0
SKIP=0
RESULTS=""

log_info()  { echo -e "\033[32m[INFO]\033[0m $1"; }
log_error() { echo -e "\033[31m[FAIL]\033[0m $1"; }

pass() { PASS=$((PASS+1)); RESULTS+="PASS: $1\n"; log_info "PASS: $1"; }
fail() { FAIL=$((FAIL+1)); RESULTS+="FAIL: $1\n"; log_error "FAIL: $1"; }
skip() { SKIP=$((SKIP+1)); RESULTS+="SKIP: $1\n"; log_info "SKIP: $1"; }

api_post() {
    curl -sf --max-time 10 -X POST "$API$1" -H "Content-Type: application/json" -d "$2"
}

api_get() {
    curl -sf --max-time 10 "$API$1"
}

api_delete() {
    curl -sf --max-time 10 -X DELETE "$API$1"
}

api_action() {
    curl -sf --max-time 10 -X POST "$API$1"
}

wait_for_port() {
    local port=$1
    local timeout=${2:-10}
    for i in $(seq 1 $timeout); do
        if ss -tlnp | grep -q ":${port} "; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_for_url() {
    local url=$1
    local timeout=${2:-10}
    for i in $(seq 1 $timeout); do
        if curl -sf --max-time 3 -o /dev/null "$url" 2>/dev/null; then
            return 0
        fi
        sleep 1
    done
    return 1
}

cleanup_all() {
    log_info "清理所有资源..."
    for pid in $(curl -sf "$API/pipes" 2>/dev/null | jq -r '.data[]?.id // empty' 2>/dev/null); do
        api_action "/pipes/$pid/stop" >/dev/null 2>&1 || true
        api_delete "/pipes/$pid" >/dev/null 2>&1 || true
    done
    for oid in $(curl -sf "$API/outputs" 2>/dev/null | jq -r '.data[]?.id // empty' 2>/dev/null); do
        api_action "/outputs/$oid/stop" >/dev/null 2>&1 || true
        api_delete "/outputs/$oid" >/dev/null 2>&1 || true
    done
    for iid in $(curl -sf "$API/inputs" 2>/dev/null | jq -r '.data[]?.id // empty' 2>/dev/null); do
        api_action "/inputs/$iid/stop" >/dev/null 2>&1 || true
        api_delete "/inputs/$iid" >/dev/null 2>&1 || true
    done
    pkill -f "ffmpeg.*pipe:0" 2>/dev/null || true
}

wait_server() {
    log_info "等待服务器启动..."
    for i in $(seq 1 30); do
        if curl -sf --max-time 3 "$API/stats" >/dev/null 2>&1; then
            log_info "服务器已就绪"
            return 0
        fi
        sleep 1
    done
    log_error "服务器启动超时"
    exit 1
}

create_file_input() {
    local name=$1
    local path=$2
    local loop=${3:-true}
    local resp
    resp=$(api_post "/inputs" "{\"name\":\"$name\",\"type\":\"file\",\"config\":\"{\\\"path\\\":\\\"$path\\\",\\\"loop\\\":$loop}\"}")
    echo "$resp" | jq -r '.data.id'
}

create_rtsp_input() {
    local name=$1
    local url=$2
    local resp
    resp=$(api_post "/inputs" "{\"name\":\"$name\",\"type\":\"rtsp\",\"config\":\"{\\\"url\\\":\\\"$url\\\",\\\"transport\\\":\\\"tcp\\\"}\"}")
    echo "$resp" | jq -r '.data.id'
}

create_rtmp_output() {
    local name=$1
    local url=$2
    local resp
    resp=$(api_post "/outputs" "{\"name\":\"$name\",\"type\":\"rtmp\",\"config\":\"{\\\"url\\\":\\\"$url\\\"}\"}")
    echo "$resp" | jq -r '.data.id'
}

create_rtsp_output() {
    local name=$1
    local mode=$2
    local addr_or_url=$3
    local config
    if [ "$mode" = "server" ]; then
        config="{\\\"mode\\\":\\\"server\\\",\\\"addr\\\":\\\"$addr_or_url\\\",\\\"transport\\\":\\\"tcp\\\"}"
    else
        config="{\\\"mode\\\":\\\"push\\\",\\\"url\\\":\\\"$addr_or_url\\\",\\\"transport\\\":\\\"tcp\\\"}"
    fi
    local resp
    resp=$(api_post "/outputs" "{\"name\":\"$name\",\"type\":\"rtsp\",\"config\":\"$config\"}")
    echo "$resp" | jq -r '.data.id'
}

create_httpflv_output() {
    local name=$1
    local addr=$2
    local resp
    resp=$(api_post "/outputs" "{\"name\":\"$name\",\"type\":\"http-flv\",\"config\":\"{\\\"addr\\\":\\\"$addr\\\"}\"}")
    echo "$resp" | jq -r '.data.id'
}

create_pipe() {
    local input_id=$1
    local output_id=$2
    local resp
    resp=$(api_post "/pipes" "{\"input_id\":\"$input_id\",\"output_id\":\"$output_id\"}")
    echo "$resp" | jq -r '.data.id'
}

start_entity() {
    local type=$1
    local id=$2
    api_action "/${type}/${id}/start" >/dev/null
}

stop_entity() {
    local type=$1
    local id=$2
    api_action "/${type}/${id}/stop" >/dev/null 2>&1 || true
}

delete_entity() {
    local type=$1
    local id=$2
    api_action "/${type}/${id}/stop" >/dev/null 2>&1 || true
    api_delete "/${type}/${id}" >/dev/null 2>&1 || true
}

# ========== T1: 文件(H264) → RTSP server ==========
test_t1() {
    log_info "=== T1: 文件(H264) → RTSP server ==="
    local input_id output_id pipe_id

    input_id=$(create_file_input "t1-h264-input" "/app/test/fixtures/h264_test.mp4")
    output_id=$(create_rtsp_output "t1-rtsp-out" "server" ":18001")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    if ffprobe -v quiet -rtsp_transport tcp -show_entries stream=codec_name -of csv=p=0 rtsp://localhost:18001/live 2>/dev/null | grep -q "h264"; then
        pass "T1: H264文件→RTSP, ffprobe检测到h264流"
    else
        fail "T1: H264文件→RTSP, ffprobe未能检测到h264流"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T2: 文件(H264) → RTMP ==========
test_t2() {
    log_info "=== T2: 文件(H264) → RTMP ==="
    local input_id output_id pipe_id

    input_id=$(create_file_input "t2-h264-input" "/app/test/fixtures/h264_test.mp4")
    output_id=$(create_rtmp_output "t2-rtmp-out" "rtmp://localhost:1935/live/t2")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    if ps aux | grep -v grep | grep -q "ffmpeg.*rtmp://localhost:1935/live/t2"; then
        pass "T2: H264文件→RTMP, ffmpeg进程运行中"
    else
        fail "T2: H264文件→RTMP, ffmpeg进程未运行"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T3: 文件(H264) → HTTP-FLV ==========
test_t3() {
    log_info "=== T3: 文件(H264) → HTTP-FLV ==="
    local input_id output_id pipe_id flv_url

    input_id=$(create_file_input "t3-h264-input" "/app/test/fixtures/h264_test.mp4")
    output_id=$(create_httpflv_output "t3-flv-out" ":18002")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    flv_url="http://localhost:18002/${output_id}.flv"
    local http_code
    http_code=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$flv_url" 2>/dev/null || echo "000")

    if [ "$http_code" = "200" ]; then
        pass "T3: H264文件→HTTP-FLV, HTTP端点返回200"
    else
        fail "T3: H264文件→HTTP-FLV, HTTP端点返回$http_code"
    fi

    if ffmpeg -i "$flv_url" -t 3 -c copy -y /tmp/t3_test.flv 2>/dev/null; then
        if ffprobe -v quiet -show_entries stream=codec_name -of csv=p=0 /tmp/t3_test.flv 2>/dev/null | grep -q "h264"; then
            pass "T3: H264文件→HTTP-FLV, 录制文件包含h264流"
        else
            fail "T3: H264文件→HTTP-FLV, 录制文件无h264流"
        fi
    else
        fail "T3: H264文件→HTTP-FLV, ffmpeg录制失败"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T4: 文件(H265) → RTSP server ==========
test_t4() {
    log_info "=== T4: 文件(H265) → RTSP server ==="
    local input_id output_id pipe_id

    input_id=$(create_file_input "t4-h265-input" "/app/test/fixtures/h265_test.mp4")
    output_id=$(create_rtsp_output "t4-rtsp-out" "server" ":18003")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    if ffprobe -v quiet -rtsp_transport tcp -show_entries stream=codec_name -of csv=p=0 rtsp://localhost:18003/live 2>/dev/null | grep -q "hevc"; then
        pass "T4: H265文件→RTSP, ffprobe检测到hevc流"
    else
        fail "T4: H265文件→RTSP, ffprobe未能检测到hevc流"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T5: 文件(H265) → RTMP ==========
test_t5() {
    log_info "=== T5: 文件(H265) → RTMP ==="
    local input_id output_id pipe_id

    input_id=$(create_file_input "t5-h265-input" "/app/test/fixtures/h265_test.mp4")
    output_id=$(create_rtmp_output "t5-rtmp-out" "rtmp://localhost:1935/live/t5")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    if ps aux | grep -v grep | grep -q "ffmpeg.*rtmp://localhost:1935/live/t5"; then
        pass "T5: H265文件→RTMP, ffmpeg进程运行中"
    else
        fail "T5: H265文件→RTMP, ffmpeg进程未运行"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T6: 文件(H265) → HTTP-FLV ==========
test_t6() {
    log_info "=== T6: 文件(H265) → HTTP-FLV ==="
    local input_id output_id pipe_id flv_url

    input_id=$(create_file_input "t6-h265-input" "/app/test/fixtures/h265_test.mp4")
    output_id=$(create_httpflv_output "t6-flv-out" ":18004")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 3

    flv_url="http://localhost:18004/${output_id}.flv"
    local http_code
    http_code=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$flv_url" 2>/dev/null || echo "000")

    if [ "$http_code" = "200" ]; then
        pass "T6: H265文件→HTTP-FLV, HTTP端点返回200"
    else
        fail "T6: H265文件→HTTP-FLV, HTTP端点返回$http_code"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T7: RTSP拉流 → RTSP server ==========
test_t7() {
    log_info "=== T7: RTSP拉流 → RTSP server ==="

    ffmpeg -re -i /app/test/fixtures/h264_test.mp4 -c copy -f rtsp rtsp://localhost:18004/source >/dev/null 2>&1 &
    local src_pid=$!
    sleep 2

    local input_id output_id pipe_id
    input_id=$(create_rtsp_input "t7-rtsp-pull" "rtsp://localhost:18004/source")
    output_id=$(create_rtsp_output "t7-rtsp-out" "server" ":18005")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 5

    if ffprobe -v quiet -rtsp_transport tcp -show_entries stream=codec_name -of csv=p=0 rtsp://localhost:18005/live 2>/dev/null | grep -q "h264"; then
        pass "T7: RTSP拉流→RTSP, ffprobe检测到h264流"
    else
        fail "T7: RTSP拉流→RTSP, ffprobe未能检测到流"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    kill $src_pid 2>/dev/null || true
    sleep 1
}

# ========== T8: RTSP拉流 → HTTP-FLV ==========
test_t8() {
    log_info "=== T8: RTSP拉流 → HTTP-FLV ==="

    ffmpeg -re -i /app/test/fixtures/h264_test.mp4 -c copy -f rtsp rtsp://localhost:18006/source >/dev/null 2>&1 &
    local src_pid=$!
    sleep 2

    local input_id output_id pipe_id flv_url
    input_id=$(create_rtsp_input "t8-rtsp-pull" "rtsp://localhost:18006/source")
    output_id=$(create_httpflv_output "t8-flv-out" ":18007")
    pipe_id=$(create_pipe "$input_id" "$output_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$output_id"
    start_entity "pipes" "$pipe_id"
    sleep 5

    flv_url="http://localhost:18007/${output_id}.flv"
    local http_code
    http_code=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$flv_url" 2>/dev/null || echo "000")

    if [ "$http_code" = "200" ]; then
        pass "T8: RTSP拉流→HTTP-FLV, HTTP端点返回200"
    else
        fail "T8: RTSP拉流→HTTP-FLV, HTTP端点返回$http_code"
    fi

    delete_entity "pipes" "$pipe_id"
    delete_entity "outputs" "$output_id"
    delete_entity "inputs" "$input_id"
    kill $src_pid 2>/dev/null || true
    sleep 1
}

# ========== T9: 文件 → 多输出 fan-out ==========
test_t9() {
    log_info "=== T9: 文件 → 多输出 fan-out ==="
    local input_id out1_id out2_id pipe1_id pipe2_id

    input_id=$(create_file_input "t9-fanout-input" "/app/test/fixtures/h264_test.mp4")
    out1_id=$(create_httpflv_output "t9-flv1" ":18008")
    out2_id=$(create_httpflv_output "t9-flv2" ":18009")

    pipe1_id=$(create_pipe "$input_id" "$out1_id")
    pipe2_id=$(create_pipe "$input_id" "$out2_id")

    start_entity "inputs" "$input_id"
    start_entity "outputs" "$out1_id"
    start_entity "outputs" "$out2_id"
    start_entity "pipes" "$pipe1_id"
    start_entity "pipes" "$pipe2_id"
    sleep 3

    local url1="http://localhost:18008/${out1_id}.flv"
    local url2="http://localhost:18009/${out2_id}.flv"
    local code1 code2
    code1=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$url1" 2>/dev/null || echo "000")
    code2=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$url2" 2>/dev/null || echo "000")

    if [ "$code1" = "200" ] && [ "$code2" = "200" ]; then
        pass "T9: fan-out, 两个输出端都正常 (HTTP $code1, $code2)"
    else
        fail "T9: fan-out, 输出端异常 (HTTP $code1, $code2)"
    fi

    delete_entity "pipes" "$pipe1_id"
    delete_entity "pipes" "$pipe2_id"
    delete_entity "outputs" "$out1_id"
    delete_entity "outputs" "$out2_id"
    delete_entity "inputs" "$input_id"
    sleep 1
}

# ========== T10: 多管道并发 ==========
test_t10() {
    log_info "=== T10: 多管道并发 ==="
    local in1_id in2_id out1_id out2_id p1_id p2_id

    in1_id=$(create_file_input "t10-h264" "/app/test/fixtures/h264_test.mp4")
    in2_id=$(create_file_input "t10-h265" "/app/test/fixtures/h265_test.mp4")
    out1_id=$(create_httpflv_output "t10-flv1" ":18010")
    out2_id=$(create_httpflv_output "t10-flv2" ":18011")

    p1_id=$(create_pipe "$in1_id" "$out1_id")
    p2_id=$(create_pipe "$in2_id" "$out2_id")

    start_entity "inputs" "$in1_id"
    start_entity "inputs" "$in2_id"
    start_entity "outputs" "$out1_id"
    start_entity "outputs" "$out2_id"
    start_entity "pipes" "$p1_id"
    start_entity "pipes" "$p2_id"
    sleep 3

    local url1="http://localhost:18010/${out1_id}.flv"
    local url2="http://localhost:18011/${out2_id}.flv"
    local code1 code2
    code1=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$url1" 2>/dev/null || echo "000")
    code2=$(curl -sf --max-time 5 -o /dev/null -w "%{http_code}" "$url2" 2>/dev/null || echo "000")

    if [ "$code1" = "200" ] && [ "$code2" = "200" ]; then
        pass "T10: 多管道并发, 两条管道都正常 (HTTP $code1, $code2)"
    else
        fail "T10: 多管道并发, 管道异常 (HTTP $code1, $code2)"
    fi

    delete_entity "pipes" "$p1_id"
    delete_entity "pipes" "$p2_id"
    delete_entity "outputs" "$out1_id"
    delete_entity "outputs" "$out2_id"
    delete_entity "inputs" "$in1_id"
    delete_entity "inputs" "$in2_id"
    sleep 1
}

# ========== Main ==========
log_info "=========================================="
log_info "X-Media 媒体协议测试"
log_info "=========================================="

wait_server
cleanup_all

test_t1
test_t2
test_t3
test_t4
test_t5
test_t6
test_t7
test_t8
test_t9
test_t10

cleanup_all

echo ""
echo "=========================================="
echo -e "测试结果: \033[32m${PASS} passed\033[0m, \033[31m${FAIL} failed\033[0m, \033[33m${SKIP} skipped\033[0m"
echo "=========================================="
echo ""
echo -e "$RESULTS"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
