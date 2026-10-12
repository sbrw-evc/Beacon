FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
ARG VERSION=dev
ARG COMMIT=
ARG BUILT_AT=
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILT_AT}" -o /umbrella ./cmd/umbrella \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /umbrella /umbrella
COPY --from=web /web/dist /web
COPY --from=backend --chown=65532:65532 /out/data /data
ENV UMBRELLA_WEB_DIR=/web UMBRELLA_ADDR=:8080 UMBRELLA_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
USER nonroot
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 CMD ["/umbrella", "-healthcheck"]
ENTRYPOINT ["/umbrella"]
