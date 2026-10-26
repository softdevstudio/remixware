"""Calls the beacon API."""


class Client:
    def __init__(self, base_url, token=None, timeout=10.0, retries=2, verify=True, user_agent="beacon"):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.retries = retries
        self.verify = verify
        self.user_agent = user_agent

    def with_options(self, token=None, timeout=None, retries=None, verify=None, user_agent=None):
        return Client(
            base_url=self.base_url,
            token=token if token is not None else self.token,
            timeout=timeout if timeout is not None else self.timeout,
            retries=retries if retries is not None else self.retries,
            verify=verify if verify is not None else self.verify,
            user_agent=user_agent if user_agent is not None else self.user_agent,
        )

    def headers(self):
        out = {"User-Agent": self.user_agent, "Accept": "application/json"}
        if self.token:
            out["Authorization"] = "Bearer " + self.token
        return out
