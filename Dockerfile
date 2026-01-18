FROM rust:1.92-slim-bookworm AS builder

WORKDIR /app

COPY . .

RUN cargo build --release

FROM debian:bookworm-slim

COPY --from=builder /app/target/release/mongodb_cmd /usr/local/bin/mongodb_cmd

CMD ["mongodb_cmd"]
