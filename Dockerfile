# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build all three service binaries: the OLTP service (execution) and the two
# analytics-side processes (projector = writer, reports = read-only reader).
# One image carries all three; each Deployment selects its binary via the
# container command (the default ENTRYPOINT runs the OLTP service).
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/execution ./cmd/execution
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/fulfillment-projector ./cmd/fulfillment-projector
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/fulfillment-reports ./cmd/fulfillment-reports

# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    addgroup -g 1000 -S app && adduser -u 1000 -S app -G app
WORKDIR /app
COPY --from=build /out/execution ./execution
COPY --from=build /out/fulfillment-projector ./fulfillment-projector
COPY --from=build /out/fulfillment-reports ./fulfillment-reports
# migrations/ carries both the OLTP migrations and migrations/analytics (the
# analytical schema the projector runs on start).
COPY --from=build /src/migrations ./migrations
USER 1000
EXPOSE 8080
ENTRYPOINT ["./execution"]
