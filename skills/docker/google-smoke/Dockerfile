ARG BASE_IMAGE=cicy-skills-base-user-env:local
FROM ${BASE_IMAGE}

WORKDIR /app

COPY . /app

RUN chmod +x /app/docker/google-smoke/smoke.sh
RUN /app/docker/google-smoke/smoke.sh

CMD ["/bin/bash"]
