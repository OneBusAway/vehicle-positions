# Vehicle Positions Android App

## Setup

1. Clone the repo
2. Copy `local.properties.example` to `local.properties`
3. Fill in your values:
   - `MAPS_API_KEY` — Google Maps API key
   - `BASE_URL` — Server base URL
   - `sdk.dir` — Android SDK path

## Building
```bash
cd android
./gradlew assembleDebug
```

## Local Testing

Start the backend server:
```bash
JWT_SECRET=devsecret12345678901234567890123 go run .
```

Get a test JWT:
```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"driver@test.com","password":"password"}'
```

Temporarily add the JWT to `TokenManager.kt` init block for testing.
JWT injection is handled by the login flow (separate PR).
