# local keycloak for dev purposes

1. copy envrc.recommended to .envrc and modify
1. run `[docker|podman] compose up`
1. open http://localhost:8080 to access keycloak

## exploring inside the keycloak container

If you want to look around inside the running keycloak
container, from a separate terminal run

    docker compose exec keycloak /bin/bash

The themes are mounted into /opt/keycloak/themes
