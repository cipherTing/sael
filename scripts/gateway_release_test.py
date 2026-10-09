import pathlib
import subprocess
import sys
import unittest

SCRIPT = pathlib.Path(__file__).with_name("gateway-release.py")


class GatewayReleaseTest(unittest.TestCase):
    def run_tag(self, tag):
        return subprocess.run([sys.executable, str(SCRIPT), tag, "sundayting/sael-gateway"], capture_output=True, text=True)

    def test_stable_publishes_exact_version_and_latest(self):
        result = self.run_tag("gateway/v1.2.3")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("version=1.2.3\nprerelease=false\n", result.stdout)
        self.assertIn("sundayting/sael-gateway:1.2.3\nsundayting/sael-gateway:latest\n", result.stdout)

    def test_preview_does_not_publish_latest(self):
        result = self.run_tag("gateway/v1.2.3-rc.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("prerelease=true\n", result.stdout)
        self.assertIn("sundayting/sael-gateway:1.2.3-rc.2", result.stdout)
        self.assertNotIn(":latest", result.stdout)

    def test_rejects_other_namespaces_and_invalid_docker_versions(self):
        for tag in ["cli/v1.2.3", "sdk/v1.2.3", "gateway/v01.2.3", "gateway/v1.2", "gateway/v1.2.3-01", "gateway/v1.2.3+build", "gateway/v1.2.3-", "gateway/v1.2.3\n"]:
            with self.subTest(tag=tag):
                result = self.run_tag(tag)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
