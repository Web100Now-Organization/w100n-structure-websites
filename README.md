# w100n_structure_websites Plugin

## Overview

`w100n_structure_websites` exposes GraphQL utilities for reading and maintaining the `structure_websites` collection that powers website layout metadata for each tenant database.

Available operations:

- `structureWebsites` – fetch all documents from the current tenant’s `structure_websites` collection.
- `updateStructureWebsite` – replace a single document (LOCAL_DEVELOPMENT only guard still applies).
- `applyStructureTemplate` – push a JSON template to every tenant that references it in `core.db_clients`.

## Fetch Current Structure

```graphql
query {
  structureWebsites {
    _id
    slug
    sections
  }
}
```

## Update a Single Document

```graphql
mutation UpdateStructure($id: ID!, $payload: JSON!) {
  updateStructureWebsite(id: $id, payload: $payload)
}
```

> ⚠️ Guarded by `LOCAL_DEVELOPMENT=true` to prevent accidental production edits.

## Apply a Template to Multiple Clients

```graphql
mutation ApplyTemplate($input: StructureTemplateInput!) {
  applyStructureTemplate(input: $input) {
    templateKey
    affectedClients
    updatedDocuments
    deletedDocuments
    message
  }
}
```

### Input

```json
{
  "input": {
    "templateKey": "template-1",
    "documents": [
      {
        "_id": "652f781b0b0f5f6c1b3dd111",
        "slug": "homepage",
        "sections": [
          {
            "type": "hero",
            "title": "Welcome!"
          }
        ]
      },
      {
        "_id": "652f781b0b0f5f6c1b3dd112",
        "slug": "menu",
        "categories": [
          {"name": "Breakfast"},
          {"name": "Lunch"}
        ]
      }
    ]
  }
}
```

### Behaviour

- The template is saved (or updated) inside the `core.structure_templates` collection for future reuse.
- Tenants are detected in `core.db_clients` where `structure_template` (or custom `targetField`) equals `templateKey`.
- If no clients are matched via `structure_template`, the resolver automatically falls back to the legacy `template` field.
- For each tenant database (`client_name`), `structure_websites` is rebuilt:
  - Documents present in the template are upserted (fields not provided are removed).
  - Documents absent from the template are deleted.
  - Empty strings / nulls in the payload are ignored (those fields are omitted).

Optional `targetField` lets you use a different flag inside `db_clients` (defaults to `structure_template`).

### Notes

- Provide `_id` values to keep document identity consistent across tenants. If `_id` is omitted a new `ObjectID` is generated and used everywhere.
- Templates run under normal role checks (platform role by default), but when `LOCAL_DEVELOPMENT=true` role checks are bypassed to simplify local testing.

