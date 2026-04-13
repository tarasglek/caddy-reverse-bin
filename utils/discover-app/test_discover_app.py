import importlib.util
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("discover-app.py")
spec = importlib.util.spec_from_file_location("discover_app", MODULE_PATH)
if spec is None or spec.loader is None:
    raise RuntimeError(f"Could not load module spec from {MODULE_PATH}")

discover_app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(discover_app)


class DiscoverAppResultTests(unittest.TestCase):
    def test_build_discovery_result_returns_expected_json_shape(self) -> None:
        # Intent: verify the typed result helper returns the exact JSON object shape emitted by discover-app.
        result = discover_app.build_discovery_result(
            executable=["./main.py"],
            reverse_proxy_to="127.0.0.1:8080",
            working_directory="/tmp/example-app",
            envs=["REVERSE_PROXY_TO=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
        )

        self.assertEqual(
            result,
            {
                "executable": ["./main.py"],
                "reverse_proxy_to": "127.0.0.1:8080",
                "working_directory": "/tmp/example-app",
                "envs": ["REVERSE_PROXY_TO=127.0.0.1:8080", "PATH=/usr/bin:/bin"],
            },
        )


if __name__ == "__main__":
    unittest.main()
