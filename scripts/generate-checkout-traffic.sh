#!/usr/bin/env bash
#
# generate-checkout-traffic.sh
# Exercises the full Online Boutique user journey (browse -> add to cart ->
# checkout -> payment) against the frontend to produce end-to-end distributed
# traces in Elastic APM.
#
# Usage:
#   FRONTEND_URL=http://boutique.assessment.local ./generate-checkout-traffic.sh [ITERATIONS]
#
# Env vars:
#   FRONTEND_URL   Base URL of the Online Boutique frontend (default: http://localhost:8080)
#   ITERATIONS     Number of full journeys to run (default: 20, or $1 if provided)
#   ERROR_RATIO    Fraction of checkouts that intentionally use an invalid card
#                  to generate error spans (default: 0.2)
#
set -euo pipefail

FRONTEND_URL="${FRONTEND_URL:-http://localhost:8080}"
ITERATIONS="${1:-${ITERATIONS:-20}}"
ERROR_RATIO="${ERROR_RATIO:-0.2}"

# Product catalog IDs shipped with Online Boutique
PRODUCTS=(OLJCESPC7Z 66VCHSJNUP 1YMWWN1N4O L9ECAV7KIM 2ZYFJ3GM2N 0PUK6V6EV0 LS4PSXUNUM 9SIQT8TOJO 6E92ZMYYFZ)
CURRENCIES=(USD EUR GBP CAD JPY)

# Persist cookies so the session id (shop_session-id) is stable across the journey,
# which keeps all requests within one logical session for trace correlation.
COOKIE_JAR="$(mktemp)"
trap 'rm -f "$COOKIE_JAR"' EXIT

# curl wrapper: follow redirects, keep session cookie, fail loudly on 5xx only.
req() {
  local method="$1"; shift
  local path="$1"; shift
  curl -sS -o /dev/null -w "%{http_code}" \
    -X "$method" \
    -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
    "$@" \
    "${FRONTEND_URL}${path}"
}

rand_from() {
  local arr=("$@")
  echo "${arr[$((RANDOM % ${#arr[@]}))]}"
}

echo "Target frontend : ${FRONTEND_URL}"
echo "Iterations      : ${ITERATIONS}"
echo "Error ratio     : ${ERROR_RATIO}"
echo "-------------------------------------------"

for i in $(seq 1 "$ITERATIONS"); do
  product="$(rand_from "${PRODUCTS[@]}")"
  currency="$(rand_from "${CURRENCIES[@]}")"
  quantity=$(( (RANDOM % 4) + 1 ))

  # 1. Browse home page (page-load / index handler)
  req GET "/" >/dev/null

  # 2. Set currency (gRPC call to currencyservice)
  req POST "/setCurrency" --data-urlencode "currency_code=${currency}" >/dev/null

  # 3. View a product (gRPC to productcatalogservice + recommendation + ads)
  req GET "/product/${product}" >/dev/null

  # 4. Add to cart (gRPC to cartservice -> Redis)
  req POST "/cart" \
    --data-urlencode "product_id=${product}" \
    --data-urlencode "quantity=${quantity}" >/dev/null

  # 5. View cart (cartservice + shipping + currency)
  req GET "/cart" >/dev/null

  # 6. Checkout -> checkoutservice -> payment/email/shipping
  # Intentionally send an invalid card for a fraction of runs to create error spans.
  use_bad_card="$(awk -v r="$ERROR_RATIO" 'BEGIN{srand(); print (rand() < r) ? 1 : 0}')"
  if [ "$use_bad_card" = "1" ]; then
    cc_number="0000-0000-0000-0000"    # fails payment validation -> ERROR span
    cc_cvv="000"
    label="checkout(EXPECT-FAIL)"
  else
    cc_number="4432-8015-6152-0454"    # test Visa accepted by paymentservice
    cc_cvv="672"
    label="checkout(ok)"
  fi

  status="$(req POST "/cart/checkout" \
    --data-urlencode "email=someone@example.com" \
    --data-urlencode "street_address=1600 Amphitheatre Parkway" \
    --data-urlencode "zip_code=94043" \
    --data-urlencode "city=Mountain View" \
    --data-urlencode "state=CA" \
    --data-urlencode "country=United States" \
    --data-urlencode "credit_card_number=${cc_number}" \
    --data-urlencode "credit_card_expiration_month=1" \
    --data-urlencode "credit_card_expiration_year=2030" \
    --data-urlencode "credit_card_cvv=${cc_cvv}")"

  printf "[%02d/%s] product=%-11s qty=%s cur=%s -> %s HTTP %s\n" \
    "$i" "$ITERATIONS" "$product" "$quantity" "$currency" "$label" "$status"

  sleep "${DELAY:-1}"
done

echo "-------------------------------------------"
echo "Done. Inspect traces in Kibana:"
echo "  Observability -> APM -> Services (frontend, cartservice, checkoutservice, paymentservice)"
echo "  Observability -> APM -> Traces  (full checkout waterfall)"
echo "  Observability -> APM -> Service Map (end-to-end call chain)"
echo "  Filter event.outcome: failure to find error transactions with exception stack traces."
