# Dev: `air` rebuilds on every .go change (source is bind-mounted by docker-compose).
# Prod: static binary on distroless, non-root.
FROM golang:1.26.10-alpine AS base
WORKDIR /src
ENV CGO_ENABLED=0

FROM base AS dev
RUN go install github.com/air-verse/air@v1.67.2
EXPOSE 9090
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot AS prod
COPY --from=build /out/server /server
EXPOSE 9090
USER nonroot
ENTRYPOINT ["/server"]
