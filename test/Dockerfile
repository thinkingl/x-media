FROM 172.16.240.100:5001/library/ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

RUN echo 'Acquire::http::Proxy "http://172.16.240.100:3142";' > /etc/apt/apt.conf.d/02proxy && \
    apt-get update && \
    apt-get install -y ffmpeg curl jq nginx libnginx-mod-rtmp net-tools procps && \
    rm -rf /var/lib/apt/lists/* && \
    rm -f /etc/apt/apt.conf.d/02proxy

RUN mkdir -p /app /app/test /app/test/fixtures /app/uploads /app/logs /app/data /run/nginx

COPY server/bin/x-media-server /app/x-media-server
COPY server/config_test.yaml /app/config.yaml
COPY test_data/h265_test.mp4 /app/test/fixtures/h265_test.mp4
COPY server/test/fixtures/test.mp4 /app/test/fixtures/h264_test.mp4
COPY test/nginx-rtmp.conf /etc/nginx/nginx.conf
COPY test/run_tests.sh /app/run_tests.sh

RUN chmod +x /app/run_tests.sh

EXPOSE 18090 1935 18001 18002 18003 18004 18005

WORKDIR /app

CMD ["bash", "-c", "nginx && ./x-media-server -c config.yaml"]
