FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /api ./cmd/api && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /migrate ./cmd/migrate && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /outbox-publisher ./cmd/outbox-publisher && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /notification-worker ./cmd/notification-worker && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /notification-dispatcher ./cmd/notification-dispatcher && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /cleanup ./cmd/cleanup

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /api /api
COPY --from=build /migrate /migrate
COPY --from=build /outbox-publisher /outbox-publisher
COPY --from=build /notification-worker /notification-worker
COPY --from=build /notification-dispatcher /notification-dispatcher
COPY --from=build /cleanup /cleanup
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
