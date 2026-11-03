FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/shop-api ./cmd/shop-api \
 && CGO_ENABLED=0 go build -trimpath -o /out/shopper ./cmd/shopper

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shop-api /out/shopper /app/
EXPOSE 8081
ENTRYPOINT ["/app/shop-api"]
