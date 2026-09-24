FROM node:22-alpine AS web
WORKDIR /src/gateway/web
COPY gateway/web/package.json gateway/web/package-lock.json ./
RUN npm ci
COPY gateway/web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go-build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
COPY sdk/ ./sdk/
COPY cli/ ./cli/
COPY gateway/ ./gateway/
RUN cd cli && CGO_ENABLED=0 go build -trimpath -o /out/sael ./cmd/sael
RUN cd gateway && CGO_ENABLED=0 go build -trimpath -o /out/sael-gateway ./cmd/sael-gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S sael && adduser -S -G sael sael && mkdir -p /var/lib/sael /opt/sael/web && chown -R sael:sael /var/lib/sael
COPY --from=go-build /out/sael /usr/local/bin/sael
COPY --from=go-build /out/sael-gateway /usr/local/bin/sael-gateway
COPY --from=web /src/gateway/web/dist/ /opt/sael/web/
USER sael
ENV WEB_DIR=/opt/sael/web SAEL_CLI_PATH=/usr/local/bin/sael SPOOL_PATH=/var/lib/sael/spool.jsonl LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/sael-gateway"]
