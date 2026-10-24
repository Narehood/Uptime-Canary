// Direct mode: the browser calls the node address typed into the sign-in form. The dashboard server answers this
// path itself instead, turning on gateway mode and, where it owns dashboard accounts and a node registry, the control
// plane (__CANARY_DASHBOARD_CONTROL_PLANE__; see src/gateway.ts). Absent here, the control plane stays off.
globalThis.__CANARY_DASHBOARD_GATEWAY__ = false;
