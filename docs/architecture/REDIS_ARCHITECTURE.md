# Redis Architecture

## Overview

Redis serves as the backbone for asynchronous job processing, caching, and session management in the EsyDocs backend. It provides high-performance, in-memory data storage with persistence capabilities.

**Version**: Redis 7 (Alpine)
**Port**: 6379
**Docker Service**: `redis`

## Core Responsibilities

1. **Job Queue Management** - Asynchronous job processing for all worker services
2. **Token Denylist** - Immediate token revocation for logout
3. **Guest Session Storage** - Anonymous user session tracking
4. **Upload Session Cache** - Chunked upload state management
5. **Rate Limiting** - Request throttling for authentication endpoints

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Redis (Port 6379)                    │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────┐  ┌──────────────────┐  ┌────────────┐ │
│  │   Job Queues    │  │  Token Denylist  │  │   Cache    │ │
│  │                 │  │                  │  │            │ │
│  │ • convert-from  │  │ • JWT revocation │  │ • Uploads  │ │
│  │ • convert-to    │  │ • Logout tokens  │  │ • Sessions │ │
│  │ • organize      │  │ • 8h TTL         │  │ • Rate     │ │
│  │ • optimize      │  │                  │  │   limits   │ │
│  │ • cleanup       │  │                  │  │            │ │
│  └─────────────────┘  └──────────────────┘  └────────────┘ │
│                                                              │
└─────────────────────────────────────────────────────────────┘
          ↑                    ↑                    ↑
          │                    │                    │
    Worker Services    Auth Middleware     Upload Service
```

## Data Structures & Key Patterns

### 1. Job Queues (Deprecated)

**Note:** Job dispatch has been migrated to NATS JetStream. Redis is no longer used for job queuing. See the Job Service and worker service documentation for NATS stream details (`JOBS_DISPATCH`, `JOBS_EVENTS`, `JOBS_DLQ`).

---

### 2. Token Denylist (Strings with TTL)

Used for immediate JWT token revocation on logout.

#### Key Format
```
denylist:jwt:{token-jti}
```

#### Example
```
denylist:jwt:550e8400-e29b-41d4-a716-446655440000
Value: "1"
TTL: 28800 seconds (8 hours - remaining token lifetime)
```

#### Operations
```bash
# Add token to denylist
SET denylist:jwt:550e8400-e29b-41d4-a716-446655440000 "1" EX 28800

# Check if token is denied
EXISTS denylist:jwt:550e8400-e29b-41d4-a716-446655440000

# View all denied tokens
KEYS denylist:jwt:*

# Get remaining TTL
TTL denylist:jwt:550e8400-e29b-41d4-a716-446655440000
```

#### Lifecycle
1. User logs out → Token JTI added to denylist
2. TTL set to remaining token lifetime (max 8 hours)
3. Middleware checks denylist on every request
4. Token expires naturally → Key auto-deleted by Redis
5. No cleanup needed (TTL handles expiration)

---

### 3. Guest Session Storage (Strings with TTL)

Tracks anonymous user sessions for guest job access.

#### Key Format
```
guest:{guest-token}:jobs
```

#### Example
```
guest:abc123-guest-token:jobs
Value: '["job-id-1","job-id-2","job-id-3"]'
TTL: 7200 seconds (2 hours)
```

#### Operations
```bash
# Create guest session with job list
SET guest:abc123-guest-token:jobs '["job-id-1"]' EX 7200

# Get guest jobs
GET guest:abc123-guest-token:jobs

# Add job to guest session
# (Requires application logic to read, modify, write back)

# Check guest session TTL
TTL guest:abc123-guest-token:jobs
```

#### Use Cases
- Anonymous users can process files without signup
- Jobs linked to guest token for 2 hours
- After expiration, jobs become inaccessible
- Guest tokens set via cookie or header

---

### 4. Upload Session Cache (Hashes)

Stores chunked upload state and metadata.

#### Key Format
```
upload:{upload-id}
```

#### Example
```
HSET upload:550e8400-e29b-41d4-a716-446655440000
  fileName "document.pdf"
  fileSize "1048576"
  totalChunks "4"
  receivedChunks "3"
  complete "false"
  userId "user-uuid"
  expiresAt "2024-01-19T12:00:00Z"
```

#### Operations
```bash
# Create upload session
HSET upload:550e8400 fileName "document.pdf" fileSize "1048576" totalChunks "4"
EXPIRE upload:550e8400 7200

# Increment received chunks
HINCRBY upload:550e8400 receivedChunks 1

# Check if complete
HGET upload:550e8400 complete

# Get all upload data
HGETALL upload:550e8400

# Delete upload session
DEL upload:550e8400
```

#### Lifecycle
1. Client calls `/api/upload/init` → Session created in Redis
2. Client uploads chunks → `receivedChunks` incremented
3. Client calls `/api/upload/complete` → Chunks assembled, session marked complete
4. Job created → Upload session deleted or marked consumed
5. TTL: 2 hours (configurable via `UPLOAD_TTL`)

---

### 5. Rate Limiting (Strings with TTL)

Tracks API request counts for rate limiting.

#### Key Format
```
ratelimit:{endpoint}:{ip-address}:{window-start}
```

#### Example
```
ratelimit:login:192.168.1.100:1705670400
Value: "3"
TTL: 60 seconds
```

#### Operations
```bash
# Increment request count
INCR ratelimit:login:192.168.1.100:1705670400
EXPIRE ratelimit:login:192.168.1.100:1705670400 60

# Check current count
GET ratelimit:login:192.168.1.100:1705670400

# Rate limit check (application logic)
# If count > 5 → Return 429 Too Many Requests
```

#### Configuration
| Endpoint | Limit | Window |
|----------|-------|--------|
| POST /auth/login | 5 requests | 60 seconds |
| POST /auth/signup | 3 requests | 60 seconds |
| POST /auth/refresh | 10 requests | 60 seconds |

---

### 6. Idempotency Keys (Strings with TTL)

Prevents duplicate job creation when clients retry requests.

#### Key Format
```
idempotency:<key>
```

| Key Pattern | Type | TTL | Purpose |
|---|---|---|---|
| `idempotency:<key>` | String | 10 minutes | Maps client idempotency key to job ID to prevent duplicate job creation |

---

### 7. Distributed Locks (Strings with SETNX)

Coordinates distributed worker processes.

#### Key Format
```
cleanup-worker:lock
```

| Key Pattern | Type | TTL | Purpose |
|---|---|---|---|
| `cleanup-worker:lock` | String (SETNX) | 10 minutes | Distributed lock ensuring only one cleanup worker runs per cycle |

---

## Performance Characteristics

### Memory Usage

| Data Type | Typical Size | Count | Total Memory |
|-----------|-------------|-------|--------------|
| Job Queue Entry | ~500 bytes | 100 jobs | ~50 KB |
| Token Denylist | ~100 bytes | 100 tokens | ~10 KB |
| Guest Session | ~200 bytes | 50 sessions | ~10 KB |
| Upload Session | ~300 bytes | 20 uploads | ~6 KB |
| Rate Limit | ~50 bytes | 1000 entries | ~50 KB |

**Estimated Total**: ~130 KB for typical load (100 concurrent operations)

### Scalability

**Current Configuration** (Single Redis Instance):
- **Throughput**: 100,000+ ops/sec (single-threaded)
- **Latency**: < 1ms for most operations
- **Memory**: 256 MB allocated (Docker default)
- **Connections**: Unlimited (connection pooling in services)

**Production Recommendations**:
- **Memory**: 1-2 GB for high traffic
- **Max Connections**: Configure per service (default: 10 per pool)
- **Persistence**: Enable AOF for job queue durability

---

## Persistence & Durability

### Current Configuration

```yaml
# docker-compose.yml
redis:
  image: redis:7-alpine
  volumes:
    - redis_data:/data  # Data persistence
```

### Persistence Modes

#### 1. RDB (Redis Database Backup) - Default
- Periodic snapshots to disk
- Fast restarts
- Potential data loss (last snapshot interval)

#### 2. AOF (Append-Only File) - Recommended for Production
```bash
# Enable in redis.conf or via command
redis-cli CONFIG SET appendonly yes
redis-cli CONFIG SET appendfsync everysec
```

**Benefits**:
- Logs every write operation
- Maximum durability (1-second data loss window)
- Slower than RDB but more reliable

**Trade-offs**:
- Larger disk usage
- Slightly slower write performance
- Longer restart time

### Backup Strategy

**Development**:
- RDB snapshots sufficient
- Data loss acceptable

**Production**:
- Enable AOF with `everysec` fsync
- Regular RDB snapshots (hourly/daily)
- Backup `/data` volume to external storage
- Consider Redis Sentinel or Cluster for HA

---

## Monitoring & Debugging

### Key Metrics to Monitor

```bash
# Memory usage
INFO memory

# Connected clients
INFO clients

# Operations per second
INFO stats

# Keyspace info (key counts by database)
INFO keyspace

# Slow queries (operations > 10ms)
SLOWLOG GET 10
```

### Health Checks

```bash
# Ping Redis
redis-cli ping
# Expected: PONG

# Check if Redis is up via Docker
docker compose exec redis redis-cli ping

# Monitor real-time commands
redis-cli MONITOR
```

### Common Debugging Commands

```bash
# View all keys (⚠️ Use carefully in production)
KEYS *

# View keys by pattern
KEYS queue:*
KEYS denylist:jwt:*
KEYS guest:*:jobs
KEYS upload:*

# Get key type
TYPE queue:word-to-pdf

# View key TTL
TTL denylist:jwt:550e8400-e29b-41d4-a716-446655440000

# Count keys
DBSIZE

# View queue contents
LRANGE queue:word-to-pdf 0 10

# View hash contents
HGETALL upload:550e8400-e29b-41d4-a716-446655440000
```

---

## Troubleshooting

### Issue: High Memory Usage

**Symptoms**: Redis using excessive memory

**Diagnosis**:
```bash
# Check memory usage
redis-cli INFO memory

# Find large keys
redis-cli --bigkeys

# Check key counts by pattern
redis-cli KEYS "queue:*" | wc -l
redis-cli KEYS "denylist:jwt:*" | wc -l
```

**Solutions**:
1. Increase `GUEST_JOB_TTL` or `UPLOAD_TTL` if too long
2. Clear old denied tokens (they should expire automatically)
3. Check for stuck jobs in queues: `LLEN queue:*`
4. Flush unused databases: `FLUSHDB` (⚠️ careful!)

---

### Issue: Jobs Not Processing

**Symptoms**: Jobs stuck in queue, not being consumed

**Diagnosis**:
```bash
# Check queue lengths
for tool in word-to-pdf pdf-to-word merge-pdf compress-pdf; do
  echo "queue:$tool: $(redis-cli LLEN queue:$tool)"
done

# View jobs in queue
redis-cli LRANGE queue:word-to-pdf 0 5
```

**Solutions**:
1. Check if worker services are running: `docker compose ps`
2. Verify workers are polling: `docker compose logs -f convert-to-pdf`
3. Check for worker crashes: `docker compose logs convert-to-pdf | grep ERROR`
4. Restart workers: `docker compose restart convert-from-pdf convert-to-pdf`

---

### Issue: Token Denylist Not Working

**Symptoms**: Logged-out users can still access API

**Diagnosis**:
```bash
# Check if token is in denylist
redis-cli EXISTS denylist:jwt:YOUR-TOKEN-JTI

# View all denied tokens
redis-cli KEYS "denylist:jwt:*"

# Check TTL
redis-cli TTL denylist:jwt:YOUR-TOKEN-JTI
```

**Solutions**:
1. Verify `AUTH_DENYLIST_ENABLED=true` in environment
2. Check JWT secret consistency across services
3. Verify token JTI is being extracted correctly
4. Check if denylist check is in middleware

---

### Issue: Upload Sessions Lost

**Symptoms**: Chunked uploads fail, "upload not found" errors

**Diagnosis**:
```bash
# Check if upload session exists
redis-cli HGETALL upload:YOUR-UPLOAD-ID

# Check TTL
redis-cli TTL upload:YOUR-UPLOAD-ID
```

**Solutions**:
1. Increase `UPLOAD_TTL` (default 2h may be too short)
2. Verify upload ID is correct
3. Check if Redis restarted (sessions are in-memory)
4. Enable AOF persistence for durability

---

## Security Considerations

### Current Configuration

```yaml
redis:
  image: redis:7-alpine
  ports:
    - "6379:6379"  # ⚠️ Exposed to localhost
  networks:
    - esydocs_net  # Internal Docker network
```

### Security Recommendations

#### Development
✅ **Current setup is fine:**
- Redis exposed only on localhost
- Internal Docker network
- No password required

#### Production
⚠️ **Apply these security measures:**

1. **Enable Authentication**:
   ```yaml
   redis:
     command: redis-server --requirepass your-secure-password
     environment:
       REDIS_PASSWORD: ${REDIS_PASSWORD}
   ```

2. **Do NOT Expose Port Publicly**:
   ```yaml
   # Remove port mapping in production
   # ports:
   #   - "6379:6379"  # Only if needed for external access
   ```

3. **Use Redis ACLs (Access Control Lists)**:
   ```bash
   # Create user with limited permissions
   redis-cli ACL SETUSER worker on >password ~queue:* +lpush +brpop
   ```

4. **Enable TLS/SSL** (for Redis 6+):
   ```bash
   redis-server --tls-port 6379 --port 0 \
     --tls-cert-file /path/to/redis.crt \
     --tls-key-file /path/to/redis.key \
     --tls-ca-cert-file /path/to/ca.crt
   ```

5. **Network Isolation**:
   - Keep Redis in internal Docker network
   - No direct internet access
   - Firewall rules to restrict connections

---

## Best Practices

### 1. Key Naming Conventions

✅ **Good**:
```
queue:word-to-pdf
denylist:jwt:550e8400
guest:abc123:jobs
upload:550e8400
ratelimit:login:192.168.1.100:1705670400
```

❌ **Bad**:
```
wordToPdfQueue
jwt_denylist_550e8400
guestJobsAbc123
```

**Rules**:
- Use colon (`:`) as namespace separator
- Lowercase with hyphens for readability
- Include entity type first (queue, denylist, etc.)
- Include identifiers last

---

### 2. TTL Management

Always set TTL for temporary data:

```bash
# ✅ Good - TTL set
SET denylist:jwt:abc123 "1" EX 28800

# ❌ Bad - No TTL, memory leak
SET denylist:jwt:abc123 "1"
```

**TTL Guidelines**:
- Token denylist: Remaining token lifetime (max 8h)
- Guest sessions: 2 hours
- Upload sessions: 2 hours
- Rate limits: 60 seconds

---

### 3. Connection Pooling

All services use connection pools:

```go
// Example Go configuration
redis.NewClient(&redis.Options{
    Addr:         "redis:6379",
    PoolSize:     10,   // Max connections
    MinIdleConns: 5,    // Keep alive
    MaxRetries:   3,
    DialTimeout:  5 * time.Second,
    ReadTimeout:  3 * time.Second,
    WriteTimeout: 3 * time.Second,
})
```

**Benefits**:
- Reduced connection overhead
- Better performance under load
- Automatic reconnection

---

### 4. Queue Processing

Workers should use blocking pops:

```go
// ✅ Good - Blocking pop with timeout
result, err := rdb.BRPop(ctx, 5*time.Second, "queue:word-to-pdf").Result()

// ❌ Bad - Polling with sleep
for {
    result, err := rdb.RPop(ctx, "queue:word-to-pdf").Result()
    if err == redis.Nil {
        time.Sleep(1 * time.Second)  // CPU waste
        continue
    }
}
```

---

## Scaling Redis

### Vertical Scaling (Single Instance)

**Current Setup**: Suitable for most workloads

**When to Scale**:
- Memory usage > 80%
- CPU usage consistently high
- Connection limits reached

**How to Scale**:
```yaml
redis:
  deploy:
    resources:
      limits:
        memory: 2G
        cpus: '2'
      reservations:
        memory: 1G
        cpus: '1'
```

---

### Horizontal Scaling (Future)

#### Option 1: Redis Sentinel (High Availability)
- Master-slave replication
- Automatic failover
- Read scaling with replicas

#### Option 2: Redis Cluster (Sharding)
- Distributed data across nodes
- Automatic sharding by key
- Scales to 1000+ nodes

#### Option 3: Managed Redis
- AWS ElastiCache
- Azure Cache for Redis
- Google Cloud Memorystore
- Redis Enterprise Cloud

---

## Related Documentation

- [Job Service](../services/JOB_SERVICE.md) - Job management and upload session management
- [Auth Service](../services/AUTH_SERVICE.md) - Token denylist usage
- [Convert From PDF](../services/CONVERT_FROM_PDF.md) - Queue consumer
- [Convert To PDF](../services/CONVERT_TO_PDF.md) - Queue consumer
- [Organize PDF](../services/ORGANIZE_PDF.md) - Queue consumer
- [Optimize PDF](../services/OPTIMIZE_PDF.md) - Queue consumer
- [Cleanup Worker](../services/CLEANUP_WORKER.md) - Session cleanup
- [Main README](../README.md) - Overall architecture

---

## Quick Reference

### Essential Commands

```bash
# Health check
docker compose exec redis redis-cli ping

# View all queues
docker compose exec redis redis-cli KEYS "queue:*"

# Check queue lengths
docker compose exec redis redis-cli LLEN queue:word-to-pdf

# View denied tokens
docker compose exec redis redis-cli KEYS "denylist:jwt:*"

# Monitor live operations
docker compose exec redis redis-cli MONITOR

# Memory info
docker compose exec redis redis-cli INFO memory

# Clear all data (⚠️ DESTRUCTIVE)
docker compose exec redis redis-cli FLUSHALL
```

### Connection String

```
redis://redis:6379/0
```

**Format**: `redis://[user]:[password]@[host]:[port]/[database]`

---

**Built with Redis 7 for high-performance job queuing and caching** ⚡
