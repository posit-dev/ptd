ARG KEYCLOAK_VERSION=26.3.2

# Build stage
FROM quay.io/keycloak/keycloak:${KEYCLOAK_VERSION} AS builder

# Enable health and metrics support
ENV KC_HEALTH_ENABLED=true
ENV KC_METRICS_ENABLED=true

# Configure database vendor
ENV KC_DB=postgres

# Configure features
ENV KC_FEATURES=token-exchange,admin-fine-grained-authz

# Configure HTTP settings
ENV KC_HTTP_RELATIVE_PATH=/auth

WORKDIR /opt/keycloak

# Copy custom theme
COPY themes/posit /opt/keycloak/themes/posit

# Build optimized Keycloak
RUN /opt/keycloak/bin/kc.sh build

# Runtime stage
FROM quay.io/keycloak/keycloak:${KEYCLOAK_VERSION}

# Copy the built Keycloak from builder stage
COPY --from=builder /opt/keycloak/ /opt/keycloak/

# Set default environment variables
ENV KC_HEALTH_ENABLED=true
ENV KC_METRICS_ENABLED=true
ENV KC_DB=postgres
ENV KC_HTTP_RELATIVE_PATH=/auth

# Expose ports
EXPOSE 8080
EXPOSE 8443

# Use the optimized startup command
ENTRYPOINT ["/opt/keycloak/bin/kc.sh"]
CMD ["start", "--optimized"]
