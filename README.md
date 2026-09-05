# VX API

## Local development

### Option A: Local PostgreSQL
1. Install PostgreSQL and create a database:
   ```bash
   sudo -u postgres psql
   CREATE DATABASE vx_db;
   CREATE USER postgres WITH PASSWORD 'postgres';
   ALTER USER postgres WITH SUPERUSER;
   \q
   ```
2. Update `.env` with your local credentials.

### Option B: Neon Database (Recommended)
1. Sign up at [Neon.tech](https://neon.tech).
2. Create a project and a branch for development (e.g., `dev`).
3. Copy the connection string from the Neon Console.
4. Update `DATABASE_URL` in your `.env` file:
   ```env
   DATABASE_URL=postgresql://user:password@ep-your-endpoint-name.region.aws.neon.tech/neondb?sslmode=require
   ```

## Production deployment

- Use a managed PostgreSQL service like **Neon** (Main branch).
- Set the `DATABASE_URL` environment variable in your production environment.
- Use a real SMTP provider for email delivery.
- **Firebase Push Notifications**: Place your `service-account.json` in the `Config/` directory.
- Run behind a reverse proxy such as Nginx or Caddy.
# vx-api
