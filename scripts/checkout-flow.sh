#!/usr/bin/env bash
# Generate checkout flow traffic to validate distributed tracing
# Usage: ./checkout-flow.sh [FRONTEND_URL]
set -euo pipefail

FRONTEND_URL="${1:-http://localhost:8080}"
SESSION_ID="test-session-$(date +%s)"
COOKIE="shop_session-id=${SESSION_ID}"

echo "==> Checkout flow validation against ${FRONTEND_URL}"
echo "    Session ID: ${SESSION_ID}"
echo ""

# Step 1: Browse — load homepage
echo "[1/6] Browse homepage"
HOME_RESP=$(curl -s -o /dev/null -w "%{http_code}" -H "Cookie: ${COOKIE}" "${FRONTEND_URL}/")
echo "      HTTP ${HOME_RESP}"

# Step 2: View product
echo "[2/6] View product"
PRODUCT_ID="OLJCESPC7Z"
PRODUCT_RESP=$(curl -s -o /dev/null -w "%{http_code}" -H "Cookie: ${COOKIE}" \
  "${FRONTEND_URL}/product/${PRODUCT_ID}")
echo "      HTTP ${PRODUCT_RESP}"

# Step 3: Add to cart
echo "[3/6] Add to cart"
CART_RESP=$(curl -s -w "\n%{http_code}" -X POST \
  -H "Cookie: ${COOKIE}" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "product_id=${PRODUCT_ID}&quantity=1" \
  "${FRONTEND_URL}/cart")
CART_CODE=$(echo "${CART_RESP}" | tail -1)
echo "      HTTP ${CART_CODE}"

# Step 4: View cart
echo "[4/6] View cart"
CART_VIEW=$(curl -s -o /dev/null -w "%{http_code}" -H "Cookie: ${COOKIE}" \
  "${FRONTEND_URL}/cart")
echo "      HTTP ${CART_VIEW}"

# Step 5: Checkout
echo "[5/6] Place order (checkout)"
ORDER_RESP=$(curl -s -w "\n%{http_code}" -X POST \
  -H "Cookie: ${COOKIE}" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "email=test@example.com&street_address=1600+Amphitheatre+Parkway&city=Mountain+View&state=CA&country=US&zip_code=94043&card_number=4432010000000008&card_exp_month=12&card_exp_year=2027&card_cvv=123" \
  "${FRONTEND_URL}/cart/checkout")
ORDER_CODE=$(echo "${ORDER_RESP}" | tail -1)
echo "      HTTP ${ORDER_CODE}"

# Step 6: Trigger a deliberate payment failure for error span validation
echo "[6/6] Trigger payment failure (invalid card)"
FAIL_RESP=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
  -H "Cookie: ${COOKIE}" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "email=fail@example.com&street_address=1+Fail+St&city=Fail&state=CA&country=US&zip_code=00000&card_number=4000000000000002&card_exp_month=01&card_exp_year=2020&card_cvv=000" \
  "${FRONTEND_URL}/cart/checkout")
echo "      HTTP ${FAIL_RESP} (expected 4xx/5xx)"

echo ""
echo "==> Validation complete"
echo ""
echo "Verify in Kibana:"
echo "  1. APM → Traces → filter service.name: frontend AND transaction.name: POST /cart/checkout"
echo "  2. Open trace waterfall — expect spans from:"
echo "     frontend → cartservice → checkoutservice → paymentservice → shippingservice"
echo "  3. APM → Service Map — verify connected services"
echo "  4. APM → Traces → filter @transaction.result: failure — inspect error tab"
echo ""
echo "Trace context check:"
echo "  curl -v -H 'Cookie: ${COOKIE}' ${FRONTEND_URL}/product/${PRODUCT_ID} 2>&1 | grep -i traceparent"
