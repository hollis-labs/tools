# AI Agent Quickstart Guide

**Audience**: AI agents, LLM-based tools, and automated code assistants
**Purpose**: Quickly consume Laravel Context Pack data to assist developers

## What is the Laravel Context Pack?

The Laravel Context Pack is a snapshot of a Laravel application's structure exported as machine-readable files. It helps you understand the application without executing code or accessing the database.

## Files You Should Read

### Essential Files (Always Present)

1. **`metadata.json`** - Start here
   - Laravel version, PHP version, database driver
   - Available tooling (Pint, Pest, PHPStan, etc.)
   - Artefact generation status

2. **`02-route-summary.json`** - Application HTTP contract
   - All routes with methods, URIs, controllers, middleware
   - Use for understanding endpoints and navigation

3. **`03-model-overview.md`** - Data layer
   - Eloquent models, database tables, relationships
   - Use for understanding data structure

### Optional Files (May Be Absent)

4. **`01-schema-dump.sql`** - Database schema
   - Table definitions, columns, foreign keys
   - Use for detailed database structure

5. **`04-static-analysis.md`** - Code quality
   - Pint, Pest, PHPStan, Rector results
   - Use for identifying code issues

## Recommended Reading Order

```
1. metadata.json          → Understand the environment
2. 02-route-summary.json  → Map the HTTP API
3. 03-model-overview.md   → Understand data models
4. 01-schema-dump.sql     → Deep dive on database (if needed)
```

## Quick Integration Patterns

### Pattern 1: Route Discovery

When a user asks "How do I update a user?", search routes:

```python
import json

def find_relevant_routes(query, routes_json_path):
    """Find routes matching a user query."""
    with open(routes_json_path) as f:
        routes = json.load(f)

    keywords = query.lower().split()
    matches = []

    for route in routes:
        searchable = ' '.join([
            str(route.get('uri', '')),
            str(route.get('name', '')),
            str(route.get('action', '')),
            str(route.get('controller', ''))
        ]).lower()

        if any(keyword in searchable for keyword in keywords):
            matches.append(route)

    return matches

# Usage
routes = find_relevant_routes(
    "update user",
    "context-pack/02-route-summary.json"
)

for route in routes:
    print(f"{route['method']} {route['uri']} -> {route['action']}")
```

### Pattern 2: API Endpoint Suggestions

When suggesting API calls, use the route JSON:

```python
def suggest_api_endpoint(action, resource, routes_json_path):
    """Suggest appropriate API endpoint for an action."""
    with open(routes_json_path) as f:
        routes = json.load(f)

    # Map common actions to HTTP methods
    action_methods = {
        'create': ['POST'],
        'update': ['PUT', 'PATCH'],
        'delete': ['DELETE'],
        'get': ['GET'],
        'list': ['GET']
    }

    target_methods = action_methods.get(action.lower(), [])

    for route in routes:
        # Check if route matches resource and method
        if (resource.lower() in route['uri'].lower() and
            any(method in route['method'] for method in target_methods)):

            return {
                'endpoint': route['uri'],
                'method': route['method'][0],
                'name': route['name'],
                'middleware': route['middleware']
            }

    return None

# Usage
endpoint = suggest_api_endpoint(
    "update",
    "users",
    "context-pack/02-route-summary.json"
)

if endpoint:
    print(f"Use: {endpoint['method']} {endpoint['endpoint']}")
    print(f"Route name: {endpoint['name']}")
```

### Pattern 3: Building Context for Prompts

Inject relevant context into your prompts:

```python
def build_route_context(routes_json_path, prefix='api'):
    """Build concise route context for LLM prompts."""
    with open(routes_json_path) as f:
        routes = json.load(f)

    # Filter to relevant routes
    api_routes = [r for r in routes if r.get('prefix') == prefix]

    context = f"Available {prefix.upper()} endpoints:\n\n"

    # Group by resource
    by_resource = {}
    for route in api_routes:
        # Extract resource from URI (e.g., "api/users/{id}" -> "users")
        uri_parts = route['uri'].split('/')
        resource = uri_parts[1] if len(uri_parts) > 1 else 'root'

        if resource not in by_resource:
            by_resource[resource] = []
        by_resource[resource].append(route)

    # Format context
    for resource, routes in sorted(by_resource.items()):
        context += f"{resource.upper()}:\n"
        for route in routes:
            methods = '|'.join(route['method'])
            context += f"  {methods} {route['uri']}"
            if route.get('name'):
                context += f" (route: {route['name']})"
            context += "\n"
        context += "\n"

    return context

# Add to your prompt
route_context = build_route_context('context-pack/02-route-summary.json')
prompt = f"""
{route_context}

User question: {user_question}

Based on the available endpoints above, provide specific guidance.
"""
```

### Pattern 4: Validation and Warnings

Alert users to potential issues:

```python
def analyze_route_quality(routes_json_path):
    """Identify route quality issues."""
    with open(routes_json_path) as f:
        routes = json.load(f)

    issues = {
        'unnamed_routes': [],
        'closure_routes': [],
        'missing_controller_files': [],
        'no_middleware': []
    }

    for route in routes:
        # Unnamed routes
        if not route.get('name'):
            issues['unnamed_routes'].append(route['uri'])

        # Closure routes
        if route.get('action') == 'Closure':
            issues['closure_routes'].append(route['uri'])

        # Missing controller files
        if route.get('controller') and not route.get('controllerPath'):
            issues['missing_controller_files'].append({
                'uri': route['uri'],
                'controller': route['controller']
            })

        # No middleware (might be intentional)
        if not route.get('middleware'):
            issues['no_middleware'].append(route['uri'])

    return issues

# Generate warnings for user
issues = analyze_route_quality('context-pack/02-route-summary.json')

if issues['unnamed_routes']:
    print(f"Warning: {len(issues['unnamed_routes'])} routes are missing names")
    print("This makes them harder to reference in code")

if issues['closure_routes']:
    print(f"Warning: {len(issues['closure_routes'])} routes use closures")
    print("Consider moving these to controller methods")
```

## Route JSON Field Reference

Each route object contains these fields:

```javascript
{
  "method": ["GET", "HEAD"],        // HTTP methods (array)
  "uri": "api/users/{id}",          // URI pattern with parameters
  "name": "api.users.show",         // Route name (null if unnamed)
  "action": "UserController@show",   // Action handler
  "controller": "App\\Http\\Controllers\\UserController",  // Full class name (null for closures)
  "controllerMethod": "show",       // Method name (null if N/A)
  "controllerPath": "app/Http/Controllers/UserController.php",  // Relative file path (null if not found)
  "middleware": ["api", "auth"],    // Middleware stack (array)
  "prefix": "api"                   // Route prefix (null if none)
}
```

**Key Points**:
- `null` values are common - always check before using
- Methods are uppercase: `["GET", "POST"]`
- Middleware array may be empty: `[]`
- Controller path may be null if file cannot be located

## Common Questions

### "How do I create/update/delete a resource?"

1. Load `02-route-summary.json`
2. Filter routes by resource name (URI contains resource)
3. Filter by method: POST for create, PUT/PATCH for update, DELETE for delete
4. Check middleware requirements (especially auth)
5. Return endpoint, method, and route name

### "What authentication is required?"

1. Load routes JSON
2. Check `middleware` array for auth-related middleware
3. Common patterns:
   - `"auth"` - session authentication
   - `"auth:sanctum"` - Sanctum API authentication
   - `"auth:api"` - API authentication
   - No auth middleware - public endpoint

### "Where is this endpoint implemented?"

1. Find route by URI or name
2. Check `controllerPath` field
3. Check `controllerMethod` field
4. If both null, it's a closure (check `action` field)

### "What data models are involved?"

1. Use controller name to infer (e.g., `UserController` → User model)
2. Cross-reference with `03-model-overview.md`
3. Check model relationships for related data

## Best Practices for AI Agents

### DO:

1. **Always validate JSON structure** - Check if fields exist before accessing
2. **Filter early** - Narrow down to relevant routes before processing
3. **Combine data sources** - Use routes + models + schema together
4. **Provide specific examples** - Show exact HTTP requests with route names
5. **Warn about issues** - Alert users to unnamed routes, missing files, etc.
6. **Cache parsed data** - Don't re-read files repeatedly

### DON'T:

1. **Don't assume field presence** - Many fields can be `null`
2. **Don't rely on array order** - Routes may be in any order
3. **Don't ignore middleware** - Auth/validation is critical
4. **Don't guess URIs** - Use exact URIs from the JSON
5. **Don't skip metadata.json** - It contains version info you may need

## Example: Complete Route Recommendation Flow

```python
import json
from typing import Optional, Dict, List

class RouteAssistant:
    """AI agent helper for Laravel route recommendations."""

    def __init__(self, context_pack_path: str):
        self.routes = self._load_routes(context_pack_path)
        self.metadata = self._load_metadata(context_pack_path)

    def _load_routes(self, path: str) -> List[Dict]:
        with open(f"{path}/02-route-summary.json") as f:
            return json.load(f)

    def _load_metadata(self, path: str) -> Dict:
        with open(f"{path}/metadata.json") as f:
            return json.load(f)

    def recommend_endpoint(self, action: str, resource: str) -> Optional[Dict]:
        """Recommend endpoint for a user action."""
        # Map user intent to HTTP methods
        method_map = {
            'create': ['POST'],
            'update': ['PUT', 'PATCH'],
            'delete': ['DELETE'],
            'get': ['GET'],
            'list': ['GET'],
            'show': ['GET']
        }

        target_methods = method_map.get(action.lower(), [])

        # Find matching routes
        candidates = []
        for route in self.routes:
            # Check if resource matches URI
            if resource.lower() in route['uri'].lower():
                # Check if method matches
                if any(m in route['method'] for m in target_methods):
                    candidates.append(route)

        if not candidates:
            return None

        # Prefer named routes
        named = [c for c in candidates if c.get('name')]
        route = named[0] if named else candidates[0]

        # Build recommendation
        return {
            'method': route['method'][0],
            'uri': route['uri'],
            'route_name': route.get('name'),
            'middleware': route.get('middleware', []),
            'requires_auth': self._requires_auth(route),
            'parameters': self._extract_parameters(route['uri']),
            'example': self._build_example(route)
        }

    def _requires_auth(self, route: Dict) -> bool:
        """Check if route requires authentication."""
        middleware = route.get('middleware', [])
        return any('auth' in m for m in middleware)

    def _extract_parameters(self, uri: str) -> List[str]:
        """Extract parameter names from URI."""
        import re
        return re.findall(r'\{([^}]+)\}', uri)

    def _build_example(self, route: Dict) -> str:
        """Build example HTTP request."""
        method = route['method'][0]
        uri = route['uri']

        # Replace parameters with examples
        uri = uri.replace('{id}', '123')
        uri = uri.replace('{user}', '1')
        uri = uri.replace('{slug}', 'example-slug')

        example = f"{method} /{uri}\n"

        if self._requires_auth(route):
            example += "Authorization: Bearer YOUR_TOKEN\n"

        return example

# Usage
assistant = RouteAssistant('context-pack')
recommendation = assistant.recommend_endpoint('update', 'user')

if recommendation:
    print(f"To update a user, use:\n")
    print(f"Route: {recommendation['route_name']}")
    print(f"Method: {recommendation['method']}")
    print(f"URI: {recommendation['uri']}")
    print(f"Parameters: {', '.join(recommendation['parameters'])}")
    print(f"\nExample request:\n{recommendation['example']}")

    if recommendation['requires_auth']:
        print("\nNote: This endpoint requires authentication")
```

## Testing Your Integration

Validate your route parsing:

```python
def test_route_parsing():
    """Test that you're correctly parsing route data."""
    # Load test route
    test_route = {
        "method": ["GET", "HEAD"],
        "uri": "api/users/{id}",
        "name": "api.users.show",
        "action": "App\\Http\\Controllers\\UserController@show",
        "controller": "App\\Http\\Controllers\\UserController",
        "controllerMethod": "show",
        "controllerPath": "app/Http/Controllers/UserController.php",
        "middleware": ["api", "auth:sanctum"],
        "prefix": "api"
    }

    # Validate assumptions
    assert isinstance(test_route['method'], list), "Methods should be array"
    assert all(m.isupper() for m in test_route['method']), "Methods should be uppercase"
    assert isinstance(test_route['middleware'], list), "Middleware should be array"

    # Handle nulls
    test_route_closure = {
        "method": ["POST"],
        "uri": "webhooks/stripe",
        "name": None,  # Can be null
        "action": "Closure",
        "controller": None,  # Can be null
        "controllerMethod": None,  # Can be null
        "controllerPath": None,  # Can be null
        "middleware": ["api"],
        "prefix": "webhooks"
    }

    # Don't fail on nulls
    assert test_route_closure['name'] is None, "Name can be null"
    assert test_route_closure['controller'] is None, "Controller can be null"

    print("All tests passed!")

test_route_parsing()
```

## Additional Resources

- **Full format specification**: [Route JSON Format Documentation](route-json-format.md)
- **Output specification**: [MVP Output Spec](mvp-output-spec.md)
- **Main README**: [../README.md](../README.md)

## Version Compatibility

This guide is compatible with:
- Laravel Context Pack: MVP version
- Route JSON format: 1.0.0
- Laravel versions: 8.x, 9.x, 10.x, 11.x

Future versions may add additional fields (backwards compatible) or change field structure (new major version).
