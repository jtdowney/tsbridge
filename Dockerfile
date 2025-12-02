FROM alpine:3.22

ARG TARGETPLATFORM
RUN apk --no-cache add ca-certificates
COPY ${TARGETPLATFORM}/tsbridge /usr/local/bin/tsbridge
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/tsbridge"]