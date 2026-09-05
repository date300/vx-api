# VX API

## Local development

1. Install PostgreSQL and create a database:
   ```bash
   sudo -u postgres psql
   CREATE DATABASE vx_db;
   CREATE USER postgres WITH PASSWORD 'postgres';
   ALTER USER postgres WITH SUPERUSER;
   \q
   ```

2. Copy the example environment file:
   ```bash
   cp .env.example .env
   ```

3. Run the API:
   ```bash
   go run main.go
   ```

The API will listen on `0.0.0.0:8080` and is reachable from your local Wi-Fi network if your firewall allows it.

## Future production deployment

- Use a managed PostgreSQL service (Render, Railway, Supabase, AWS RDS, etc.)
- Set production environment variables securely
- Use a real SMTP provider for email delivery
- Run behind a reverse proxy such as Nginx or Caddy
# vx-api
