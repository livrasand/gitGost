#!/bin/bash

# Test script for gitGost server
echo "🧪 Testing gitGost Server"
echo "=========================="
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test 1: Health endpoint
echo "1️⃣  Testing /health endpoint..."
HEALTH_RESPONSE=$(curl -s http://localhost:8080/health)
if echo "$HEALTH_RESPONSE" | grep -q "healthy"; then
    echo -e "${GREEN}✓ Health check passed${NC}"
    echo "   Response: $HEALTH_RESPONSE"
else
    echo -e "${RED}✗ Health check failed${NC}"
    echo "   Response: $HEALTH_RESPONSE"
fi
echo ""

# Test 2: Metrics endpoint
echo "2️⃣  Testing /metrics endpoint..."
METRICS_RESPONSE=$(curl -s http://localhost:8080/metrics)
if echo "$METRICS_RESPONSE" | grep -q "goroutines"; then
    echo -e "${GREEN}✓ Metrics endpoint passed${NC}"
    echo "   Response: $METRICS_RESPONSE"
else
    echo -e "${RED}✗ Metrics endpoint failed${NC}"
    echo "   Response: $METRICS_RESPONSE"
fi
echo ""

# Test 3: Web interface
echo "3️⃣  Testing web interface..."
WEB_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/)
if [ "$WEB_RESPONSE" = "200" ]; then
    echo -e "${GREEN}✓ Web interface accessible (HTTP $WEB_RESPONSE)${NC}"
else
    echo -e "${RED}✗ Web interface failed (HTTP $WEB_RESPONSE)${NC}"
fi
echo ""

# Test 4: API authentication (if API key is set)
echo "4️⃣  Testing API authentication..."
if [ -n "$GITGOST_API_KEY" ]; then
    # Test without API key (should fail)
    AUTH_FAIL=$(curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8080/v1/gh/test/repo/git-receive-pack)
    if [ "$AUTH_FAIL" = "401" ]; then
        echo -e "${GREEN}✓ Authentication required (HTTP $AUTH_FAIL)${NC}"
    else
        echo -e "${YELLOW}⚠ Expected 401, got HTTP $AUTH_FAIL${NC}"
    fi
    
    # Test with API key (should pass validation, may fail for other reasons)
    AUTH_PASS=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
        -H "X-Gitgost-Key: $GITGOST_API_KEY" \
        http://localhost:8080/v1/gh/test/repo/git-receive-pack)
    echo "   With API key: HTTP $AUTH_PASS"
else
    echo -e "${YELLOW}⚠ GITGOST_API_KEY not set, skipping auth test${NC}"
fi
echo ""

# Test 5: GitHub token configuration
echo "5️⃣  Checking GitHub token configuration..."
if [ -n "$GITHUB_TOKEN" ]; then
    echo -e "${GREEN}✓ GITHUB_TOKEN is configured${NC}"
    echo "   Token: ${GITHUB_TOKEN:0:10}..."
else
    echo -e "${RED}✗ GITHUB_TOKEN is not set${NC}"
fi
echo ""

# Summary
echo "=========================="
echo "✅ Server is running on http://localhost:8080"
echo ""
echo "📝 Available endpoints:"
echo "   • GET  /health"
echo "   • GET  /metrics"
echo "   • GET  /"
echo "   • POST /v1/gh/:owner/:repo/git-receive-pack"
echo ""
echo "🔧 Configuration:"
echo "   • Port: ${PORT:-8080}"
echo "   • GitHub Token: ${GITHUB_TOKEN:+configured}"
echo "   • API Key: ${GITGOST_API_KEY:+configured}"
echo ""