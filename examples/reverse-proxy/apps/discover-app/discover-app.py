#!/usr/bin/env python3
import json

print(
    json.dumps(
        {
            "executable": ["python3", "-m", "http.server", "23232"],
            "reverse_proxy_to": ":23232",
            # also put first arg as working dir...ensure it exists AI!
        }
    )
)
