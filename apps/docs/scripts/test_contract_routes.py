"""Regression coverage for nested router helpers and exact namespace inheritance."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("routes", Path(__file__).with_name("verify-contract-routes.py"))
routes = importlib.util.module_from_spec(spec)
spec.loader.exec_module(routes)


class RouteTests(unittest.TestCase):
    def test_nested_registration_inherits_parent_scope(self):
        region = routes.Region('''router.Route("/core/v1", func(r chi.Router) {
            r.Group(func(inner chi.Router) {
                h.registerNestedRoutes(inner)
            })
        })''')
        self.assertEqual(region.calls({"router": ""}), [("registerNestedRoutes", "/core/v1")])
        child = routes.Region('r.Get("/projects/{project_id}", h.getProject)')
        self.assertEqual(child.routes({"r": "/core/v1"}), [("/core/v1/projects/{project_id}", "GET")])

    def test_unrelated_router_does_not_acquire_a_prefix(self):
        region = routes.Region('unknown.Get("/projects", handler)')
        self.assertEqual(region.routes({"r": "/core/v1"}), [])


if __name__ == "__main__":
    unittest.main()
