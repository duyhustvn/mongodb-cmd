## Swagger docs 
- Install swaggo
```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

- Generate docs
```
swag init --parseDependency --parseInternal
```

- Access swagger ui at http://localhost:8082/mongo-cmd/swagger/index.html

## Snapshot Index
Nếu collection index_snapshots đã tồn tại từ trước, MongoDB không cho phép sửa expireAfterSeconds bằng CreateIndex -> sẽ bị lỗi. Để áp dụng TTL cho collection cũ, cần chạy lệnh này trong mongo shell
```
db.runCommand({
  collMod: "index_snapshots",
  index: { keyPattern: { captured_at: 1 }, expireAfterSeconds: 7776000 }
})
// 7776000 = 90 ngày * 24h * 3600s
```