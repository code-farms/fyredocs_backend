# 🚀 Quick Start Guide

## Deploy Everything in One Command

### Linux/Mac/Git Bash:
```bash
chmod +x deploy.sh
./deploy.sh
```

### Windows (PowerShell/CMD):
```cmd
deploy.bat
```

That's it! 🎉

---

## What Just Happened?

✅ **Generated** a secure JWT secret
✅ **Started** 7 services (API Gateway, Upload Service, PostgreSQL, Redis, + 3 workers)
✅ **Configured** all security features (rate limiting, token denylist, HTTPS ready)
✅ **Exposed** services on localhost ports

---

## Test It Out

### 1. Health Check
```bash
curl http://localhost:8080/healthz
curl http://localhost:8081/healthz
```

### 2. Signup
```bash
curl -X POST http://localhost:8081/auth/signup \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "securePassword123",
    "fullName": "Test User",
    "country": "US"
  }'
```

### 3. Login
```bash
curl -X POST http://localhost:8081/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "test@example.com",
    "password": "securePassword123"
  }'
```

Save the `accessToken` from the response!

### 4. Access Protected Endpoint
```bash
curl http://localhost:8081/auth/me \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN_HERE"
```

### 5. Test Rate Limiting
```bash
# Try to login 6 times with wrong password
for i in {1..6}; do
  curl -X POST http://localhost:8081/auth/login \
    -H "Content-Type: application/json" \
    -d '{"email":"test@example.com","password":"wrong"}'
  echo ""
done
# 6th request should return 429 Too Many Requests
```

### 6. Logout (with Token Denylist)
```bash
curl -X POST http://localhost:8081/auth/logout \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN_HERE"

# Try to use the same token again - should fail with 401
curl http://localhost:8081/auth/me \
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN_HERE"
```

---

## View Logs

```bash
# All services
docker compose logs -f

# Just one service
docker compose logs -f api-gateway
```

---

## Stop Everything

```bash
docker compose down
```

---

## Need Help?

See [DEPLOYMENT.md](DEPLOYMENT.md) for detailed documentation.

---

## Security Features Enabled ✅

| Feature | Status | What It Does |
|---------|--------|--------------|
| JWT Secret Validation | ✅ | Services won't start without a valid 256-bit secret |
| HTTPS Cookies | ⚠️ | Enabled in production (disabled for localhost) |
| Rate Limiting | ✅ | 5 login attempts/min, 3 signups/min |
| Token Denylist | ✅ | Logout immediately invalidates tokens |
| Password Hashing | ✅ | bcrypt with proper cost |
| CORS Protection | ✅ | Configured for your frontend |

---

## Troubleshooting

**Services won't start?**
```bash
docker compose logs api-gateway
docker compose logs upload-service
```

**Need to reset everything?**
```bash
docker compose down -v  # ⚠️ Deletes all data
./deploy.sh
```

**JWT secret issues?**
```bash
rm .jwt_secret
./deploy.sh  # Will generate a new one
```

---

## What's Next?

1. ✅ Connect your frontend to `http://localhost:8080`
2. ✅ Test the authentication flow
3. ✅ Review security settings in `docker-compose.yml`
4. ✅ For production, see [DEPLOYMENT.md](DEPLOYMENT.md)
