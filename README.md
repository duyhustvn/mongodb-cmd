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

