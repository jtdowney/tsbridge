FROM alpine:3.23

RUN apk --no-cache add ca-certificates
COPY tsbridge /usr/local/bin/tsbridge
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/tsbridge"]
