FROM alpine:3.21 AS build

RUN apk add --no-cache go

WORKDIR /build
COPY . /build

RUN go build -o teams-status-notifier

FROM alpine:3.21
COPY --from=build /build/teams-status-notifier /teams-status-notifier

ENTRYPOINT /teams-status-notifier