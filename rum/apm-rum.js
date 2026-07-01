/**
 * Elastic APM RUM Agent integration for Online Boutique frontend
 *
 * Features:
 *   - Page load transactions with document timing marks
 *   - Fetch/XHR auto-instrumentation with distributed trace headers
 *   - User interaction tracing (Add to Cart, Checkout clicks)
 *   - Core Web Vitals: LCP, FID, CLS, TTFB
 *   - Custom context: page route, session ID, device classification
 */

import { init as initApm } from '@elastic/apm-rum';

const APM_SERVER_URL = window.__APM_SERVER_URL__ || '/apm';
const ENVIRONMENT = window.__DEPLOYMENT_ENV__ || 'assessment';

// Session ID — persisted across page loads
function getSessionId() {
  let sessionId = sessionStorage.getItem('rum_session_id');
  if (!sessionId) {
    sessionId = `sess_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
    sessionStorage.setItem('rum_session_id', sessionId);
  }
  return sessionId;
}

// Device classification
function getDeviceType() {
  const ua = navigator.userAgent;
  if (/Mobi|Android|iPhone|iPad/i.test(ua)) return 'mobile';
  if (/Tablet|iPad/i.test(ua)) return 'tablet';
  return 'desktop';
}

const apm = initApm({
  serviceName: 'frontend-rum',
  serverUrl: APM_SERVER_URL,
  serviceVersion: 'v0.3.9',
  environment: ENVIRONMENT,
  active: true,

  // Distributed tracing — inject traceparent on all outbound requests
  distributedTracing: true,
  distributedTracingOrigins: [
    window.location.origin,
    /https?:\/\/.*\.boutique\.local/,
    /https?:\/\/.*\.assessment\.local/,
  ],

  // Performance monitoring
  breakdownMetrics: true,

  // Capture unhandled errors and promise rejections
  captureErrors: true,

  // Page load instrumentation
  pageLoadTransactionName: () => {
    const route = window.location.pathname;
    return `page-load:${route}`;
  },
});

// Custom labels on all transactions
apm.addLabels({
  'session.id': getSessionId(),
  'device.type': getDeviceType(),
  'page.route': window.location.pathname,
  'user.agent': navigator.userAgent,
});

// Update route label on SPA navigation
function onRouteChange(route) {
  apm.addLabels({ 'page.route': route });
  const transaction = apm.startTransaction(`page-load:${route}`, 'page-load');
  if (transaction) {
    transaction.addLabels({
      'session.id': getSessionId(),
      'device.type': getDeviceType(),
    });
    // End after paint
    requestAnimationFrame(() => {
      setTimeout(() => transaction.end(), 0);
    });
  }
}

// User interaction tracing for key UI elements
function instrumentUserInteractions() {
  document.addEventListener('click', (event) => {
    const target = event.target.closest('[data-rum-action]');
    if (!target) return;

    const action = target.getAttribute('data-rum-action');
    const span = apm.startSpan(action, 'user-interaction');

    if (span) {
      span.addLabels({
        'ui.element': target.tagName.toLowerCase(),
        'ui.action': action,
        'page.route': window.location.pathname,
        'session.id': getSessionId(),
      });

      // Correlate with backend: span inherits active transaction's trace context
      // fetch/XHR calls made within click handler will carry traceparent header
      const productId = target.getAttribute('data-product-id');
      if (productId) {
        span.addLabels({ 'product.id': productId });
      }

      // End span after a short delay to capture async fetch
      setTimeout(() => span.end(), 5000);
    }
  }, true);
}

// Core Web Vitals via PerformanceObserver
function observeWebVitals() {
  // LCP — Largest Contentful Paint
  if ('PerformanceObserver' in window) {
    try {
      const lcpObserver = new PerformanceObserver((list) => {
        const entries = list.getEntries();
        const lastEntry = entries[entries.length - 1];
        if (lastEntry) {
          apm.addLabels({ 'web.vital.lcp': lastEntry.startTime });
        }
      });
      lcpObserver.observe({ type: 'largest-contentful-paint', buffered: true });
    } catch (_) { /* not supported */ }

    // FID — First Input Delay
    try {
      const fidObserver = new PerformanceObserver((list) => {
        const entry = list.getEntries()[0];
        if (entry) {
          apm.addLabels({ 'web.vital.fid': entry.processingStart - entry.startTime });
        }
      });
      fidObserver.observe({ type: 'first-input', buffered: true });
    } catch (_) { /* not supported */ }

    // CLS — Cumulative Layout Shift
    let clsValue = 0;
    try {
      const clsObserver = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (!entry.hadRecentInput) {
            clsValue += entry.value;
          }
        }
        apm.addLabels({ 'web.vital.cls': clsValue });
      });
      clsObserver.observe({ type: 'layout-shift', buffered: true });
    } catch (_) { /* not supported */ }
  }

  // TTFB — Time to First Byte
  window.addEventListener('load', () => {
    const navTiming = performance.getEntriesByType('navigation')[0];
    if (navTiming) {
      const ttfb = navTiming.responseStart - navTiming.requestStart;
      apm.addLabels({ 'web.vital.ttfb': ttfb });
    }
  });
}

// Initialize on DOM ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => {
    instrumentUserInteractions();
    observeWebVitals();
  });
} else {
  instrumentUserInteractions();
  observeWebVitals();
}

export { apm, onRouteChange, getSessionId, getDeviceType };
