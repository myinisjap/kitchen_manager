# Kitchen Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-page, mobile-accessible web app for tracking household food inventory with shopping lists, recipe integration, and weekly meal calendar planning.

**Architecture:** A Python FastAPI backend serves a REST API with SQLite for persistence; the frontend is a single HTML file with vanilla JS + Alpine.js for reactivity and Tailwind CSS for mobile-first styling. All state lives in the database; the frontend fetches and updates via JSON API calls. No build step required.

**Tech Stack:** Python 3.11+, FastAPI, SQLite (via `aiosqlite` + `databases`), Pydantic v2, Uvicorn, Alpine.js (CDN), Tailwind CSS (CDN Play), `python-barcode` (stub, deferred), `pytest` + `httpx` for tests.

---

## File Structure

```
kitchen_manager/
├── main.py                    # FastAPI app entry point, mounts static files
├── database.py                # DB connection, table creation, migration helpers
├── models.py                  # Pydantic schemas (request/response models)
├── routers/
│   ├── __init__.py
│   ├── inventory.py           # CRUD for inventory items
│   ├── shopping.py            # Shopping list endpoints
│   ├── recipes.py             # Recipe CRUD + ingredient management
│   └── calendar.py            # Meal calendar endpoints + weekly shopping
├── services/
│   ├── __init__.py
│   ├── shopping_service.py    # Logic: threshold checks → list items
│   └── calendar_service.py   # Logic: weekly plan → shopping list delta
├── static/
│   └── index.html             # Single-page app (Alpine.js + Tailwind CDN)
├── tests/
│   ├── conftest.py            # pytest fixtures: test DB, test client
│   ├── test_inventory.py
│   ├── test_shopping.py
│   ├── test_recipes.py
│   └── test_calendar.py
├── requirements.txt
└── .gitignore
```

---

## Task 1: Project Scaffold & Dependencies

**Files:**
- Create: `requirements.txt`
- Create: `.gitignore`
- Create: `main.py`
- Create: `database.py`

- [ ] **Step 1: Create requirements.txt**

```
fastapi==0.115.0
uvicorn[standard]==0.30.6
aiosqlite==0.20.0
databases[aiosqlite]==0.9.0
pydantic==2.9.2
httpx==0.27.2
pytest==8.3.3
pytest-asyncio==0.24.0
anyio==4.6.0
```

- [ ] **Step 2: Install dependencies**

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
```

Expected: All packages install without error.

- [ ] **Step 3: Create .gitignore**

```
.venv/
__pycache__/
*.pyc
*.db
.pytest_cache/
```

- [ ] **Step 4: Create database.py**

```python
import databases
import sqlalchemy

DATABASE_URL = "sqlite+aiosqlite:///./kitchen.db"

database = databases.Database(DATABASE_URL)
metadata = sqlalchemy.MetaData()

inventory_table = sqlalchemy.Table(
    "inventory",
    metadata,
    sqlalchemy.Column("id", sqlalchemy.Integer, primary_key=True),
    sqlalchemy.Column("name", sqlalchemy.String, nullable=False),
    sqlalchemy.Column("quantity", sqlalchemy.Float, nullable=False, default=0.0),
    sqlalchemy.Column("unit", sqlalchemy.String, nullable=False, default=""),
    sqlalchemy.Column("location", sqlalchemy.String, nullable=False, default=""),
    sqlalchemy.Column("expiration_date", sqlalchemy.String, nullable=True),
    sqlalchemy.Column("low_threshold", sqlalchemy.Float, nullable=False, default=1.0),
    sqlalchemy.Column("barcode", sqlalchemy.String, nullable=True),
)

shopping_list_table = sqlalchemy.Table(
    "shopping_list",
    metadata,
    sqlalchemy.Column("id", sqlalchemy.Integer, primary_key=True),
    sqlalchemy.Column("inventory_id", sqlalchemy.Integer, sqlalchemy.ForeignKey("inventory.id"), nullable=True),
    sqlalchemy.Column("name", sqlalchemy.String, nullable=False),
    sqlalchemy.Column("quantity_needed", sqlalchemy.Float, nullable=False, default=1.0),
    sqlalchemy.Column("unit", sqlalchemy.String, nullable=False, default=""),
    sqlalchemy.Column("checked", sqlalchemy.Boolean, nullable=False, default=False),
    sqlalchemy.Column("source", sqlalchemy.String, nullable=False, default="manual"),  # "manual", "threshold", "recipe", "calendar"
)

recipes_table = sqlalchemy.Table(
    "recipes",
    metadata,
    sqlalchemy.Column("id", sqlalchemy.Integer, primary_key=True),
    sqlalchemy.Column("name", sqlalchemy.String, nullable=False),
    sqlalchemy.Column("description", sqlalchemy.String, nullable=True),
    sqlalchemy.Column("instructions", sqlalchemy.String, nullable=True),
    sqlalchemy.Column("tags", sqlalchemy.String, nullable=True),  # comma-separated
    sqlalchemy.Column("servings", sqlalchemy.Integer, nullable=False, default=1),
)

recipe_ingredients_table = sqlalchemy.Table(
    "recipe_ingredients",
    metadata,
    sqlalchemy.Column("id", sqlalchemy.Integer, primary_key=True),
    sqlalchemy.Column("recipe_id", sqlalchemy.Integer, sqlalchemy.ForeignKey("recipes.id"), nullable=False),
    sqlalchemy.Column("inventory_id", sqlalchemy.Integer, sqlalchemy.ForeignKey("inventory.id"), nullable=True),
    sqlalchemy.Column("name", sqlalchemy.String, nullable=False),
    sqlalchemy.Column("quantity", sqlalchemy.Float, nullable=False),
    sqlalchemy.Column("unit", sqlalchemy.String, nullable=False, default=""),
)

meal_calendar_table = sqlalchemy.Table(
    "meal_calendar",
    metadata,
    sqlalchemy.Column("id", sqlalchemy.Integer, primary_key=True),
    sqlalchemy.Column("date", sqlalchemy.String, nullable=False),  # ISO date: YYYY-MM-DD
    sqlalchemy.Column("meal_slot", sqlalchemy.String, nullable=False, default="dinner"),  # breakfast/lunch/dinner
    sqlalchemy.Column("recipe_id", sqlalchemy.Integer, sqlalchemy.ForeignKey("recipes.id"), nullable=False),
    sqlalchemy.Column("servings", sqlalchemy.Integer, nullable=False, default=1),
)

engine = sqlalchemy.create_engine(
    DATABASE_URL.replace("+aiosqlite", ""), connect_args={"check_same_thread": False}
)


def create_tables():
    metadata.create_all(engine)
```

- [ ] **Step 5: Create main.py**

```python
from contextlib import asynccontextmanager
from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles
from fastapi.responses import FileResponse
from database import database, create_tables
from routers import inventory, shopping, recipes, calendar


@asynccontextmanager
async def lifespan(app: FastAPI):
    create_tables()
    await database.connect()
    yield
    await database.disconnect()


app = FastAPI(title="Kitchen Manager", lifespan=lifespan)

app.include_router(inventory.router, prefix="/api/inventory", tags=["inventory"])
app.include_router(shopping.router, prefix="/api/shopping", tags=["shopping"])
app.include_router(recipes.router, prefix="/api/recipes", tags=["recipes"])
app.include_router(calendar.router, prefix="/api/calendar", tags=["calendar"])

app.mount("/static", StaticFiles(directory="static"), name="static")


@app.get("/")
async def serve_spa():
    return FileResponse("static/index.html")
```

- [ ] **Step 6: Create routers/__init__.py and services/__init__.py**

```bash
mkdir -p routers services static tests
touch routers/__init__.py services/__init__.py
```

- [ ] **Step 7: Create a minimal placeholder index.html to allow startup**

```bash
mkdir -p static
```

`static/index.html`:
```html
<!DOCTYPE html>
<html><body><h1>Kitchen Manager</h1></body></html>
```

- [ ] **Step 8: Verify app starts**

```bash
uvicorn main:app --reload
```

Expected: `Application startup complete.` with no errors. Visit `http://localhost:8000` and see "Kitchen Manager".

- [ ] **Step 9: Commit**

```bash
git init
git add .
git commit -m "feat: project scaffold with FastAPI, SQLite schema, and static file serving"
```

---

## Task 2: Inventory API

**Files:**
- Create: `models.py`
- Create: `routers/inventory.py`
- Create: `tests/conftest.py`
- Create: `tests/test_inventory.py`

- [ ] **Step 1: Create models.py**

```python
from pydantic import BaseModel
from typing import Optional


class InventoryItemCreate(BaseModel):
    name: str
    quantity: float = 0.0
    unit: str = ""
    location: str = ""
    expiration_date: Optional[str] = None  # ISO date string YYYY-MM-DD
    low_threshold: float = 1.0
    barcode: Optional[str] = None


class InventoryItemUpdate(BaseModel):
    name: Optional[str] = None
    quantity: Optional[float] = None
    unit: Optional[str] = None
    location: Optional[str] = None
    expiration_date: Optional[str] = None
    low_threshold: Optional[float] = None
    barcode: Optional[str] = None


class InventoryItem(InventoryItemCreate):
    id: int

    class Config:
        from_attributes = True


class ShoppingListItemCreate(BaseModel):
    name: str
    quantity_needed: float = 1.0
    unit: str = ""
    inventory_id: Optional[int] = None
    source: str = "manual"


class ShoppingListItemUpdate(BaseModel):
    checked: Optional[bool] = None
    quantity_needed: Optional[float] = None


class ShoppingListItem(ShoppingListItemCreate):
    id: int
    checked: bool

    class Config:
        from_attributes = True


class RecipeIngredientCreate(BaseModel):
    name: str
    quantity: float
    unit: str = ""
    inventory_id: Optional[int] = None


class RecipeIngredient(RecipeIngredientCreate):
    id: int
    recipe_id: int

    class Config:
        from_attributes = True


class RecipeCreate(BaseModel):
    name: str
    description: Optional[str] = None
    instructions: Optional[str] = None
    tags: Optional[str] = None
    servings: int = 1
    ingredients: list[RecipeIngredientCreate] = []


class RecipeUpdate(BaseModel):
    name: Optional[str] = None
    description: Optional[str] = None
    instructions: Optional[str] = None
    tags: Optional[str] = None
    servings: Optional[int] = None


class Recipe(BaseModel):
    id: int
    name: str
    description: Optional[str]
    instructions: Optional[str]
    tags: Optional[str]
    servings: int
    ingredients: list[RecipeIngredient] = []

    class Config:
        from_attributes = True


class MealCalendarEntryCreate(BaseModel):
    date: str  # YYYY-MM-DD
    meal_slot: str = "dinner"
    recipe_id: int
    servings: int = 1


class MealCalendarEntry(MealCalendarEntryCreate):
    id: int

    class Config:
        from_attributes = True


class WeeklyShoppingResponse(BaseModel):
    week_start: str
    week_end: str
    shopping_items: list[ShoppingListItem]
```

- [ ] **Step 2: Create tests/conftest.py**

```python
import pytest
import pytest_asyncio
from httpx import AsyncClient, ASGITransport
from sqlalchemy import create_engine
import databases

TEST_DATABASE_URL = "sqlite+aiosqlite:///./test_kitchen.db"


@pytest.fixture(scope="session", autouse=True)
def setup_test_db(tmp_path_factory):
    import database as db_module
    db_module.DATABASE_URL = TEST_DATABASE_URL
    db_module.database = databases.Database(TEST_DATABASE_URL)
    sync_url = TEST_DATABASE_URL.replace("+aiosqlite", "")
    db_module.engine = create_engine(sync_url, connect_args={"check_same_thread": False})
    db_module.metadata.create_all(db_module.engine)
    yield
    db_module.metadata.drop_all(db_module.engine)


@pytest_asyncio.fixture
async def client():
    from main import app
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as ac:
        yield ac
```

Add `pytest.ini` at project root:
```ini
[pytest]
asyncio_mode = auto
```

- [ ] **Step 3: Write failing inventory tests**

`tests/test_inventory.py`:
```python
import pytest


@pytest.mark.asyncio
async def test_create_inventory_item(client):
    response = await client.post("/api/inventory/", json={
        "name": "Milk",
        "quantity": 1.0,
        "unit": "gallon",
        "location": "fridge",
        "expiration_date": "2026-04-01",
        "low_threshold": 0.5,
    })
    assert response.status_code == 201
    data = response.json()
    assert data["name"] == "Milk"
    assert data["id"] is not None


@pytest.mark.asyncio
async def test_list_inventory_items(client):
    await client.post("/api/inventory/", json={"name": "Eggs", "quantity": 12, "unit": "count", "location": "fridge"})
    response = await client.get("/api/inventory/")
    assert response.status_code == 200
    items = response.json()
    assert any(i["name"] == "Eggs" for i in items)


@pytest.mark.asyncio
async def test_update_inventory_item(client):
    create = await client.post("/api/inventory/", json={"name": "Butter", "quantity": 2.0, "unit": "stick", "location": "fridge"})
    item_id = create.json()["id"]
    response = await client.patch(f"/api/inventory/{item_id}", json={"quantity": 1.0})
    assert response.status_code == 200
    assert response.json()["quantity"] == 1.0


@pytest.mark.asyncio
async def test_delete_inventory_item(client):
    create = await client.post("/api/inventory/", json={"name": "ToDelete", "quantity": 1.0, "unit": "", "location": ""})
    item_id = create.json()["id"]
    response = await client.delete(f"/api/inventory/{item_id}")
    assert response.status_code == 204
    get = await client.get(f"/api/inventory/{item_id}")
    assert get.status_code == 404


@pytest.mark.asyncio
async def test_get_expiring_soon(client):
    await client.post("/api/inventory/", json={
        "name": "Yogurt",
        "quantity": 1.0,
        "unit": "cup",
        "location": "fridge",
        "expiration_date": "2026-03-29",  # 2 days from now (2026-03-27)
    })
    response = await client.get("/api/inventory/expiring?days=3")
    assert response.status_code == 200
    items = response.json()
    assert any(i["name"] == "Yogurt" for i in items)
```

- [ ] **Step 4: Run tests to verify they fail**

```bash
pytest tests/test_inventory.py -v
```

Expected: All tests FAIL with 404/import errors — routers/inventory.py does not exist yet.

- [ ] **Step 5: Create routers/inventory.py**

```python
from fastapi import APIRouter, HTTPException, status
from database import database, inventory_table
from models import InventoryItem, InventoryItemCreate, InventoryItemUpdate
from datetime import date, timedelta

router = APIRouter()


@router.post("/", response_model=InventoryItem, status_code=status.HTTP_201_CREATED)
async def create_item(item: InventoryItemCreate):
    query = inventory_table.insert().values(**item.model_dump())
    item_id = await database.execute(query)
    return {**item.model_dump(), "id": item_id}


@router.get("/", response_model=list[InventoryItem])
async def list_items(location: str = None, name: str = None):
    query = inventory_table.select()
    if location:
        query = query.where(inventory_table.c.location == location)
    if name:
        query = query.where(inventory_table.c.name.ilike(f"%{name}%"))
    rows = await database.fetch_all(query)
    return [dict(r) for r in rows]


@router.get("/expiring", response_model=list[InventoryItem])
async def get_expiring(days: int = 7):
    cutoff = (date.today() + timedelta(days=days)).isoformat()
    today = date.today().isoformat()
    query = inventory_table.select().where(
        inventory_table.c.expiration_date != None
    ).where(
        inventory_table.c.expiration_date <= cutoff
    ).where(
        inventory_table.c.expiration_date >= today
    )
    rows = await database.fetch_all(query)
    return [dict(r) for r in rows]


@router.get("/{item_id}", response_model=InventoryItem)
async def get_item(item_id: int):
    query = inventory_table.select().where(inventory_table.c.id == item_id)
    row = await database.fetch_one(query)
    if not row:
        raise HTTPException(status_code=404, detail="Item not found")
    return dict(row)


@router.patch("/{item_id}", response_model=InventoryItem)
async def update_item(item_id: int, item: InventoryItemUpdate):
    existing = await database.fetch_one(inventory_table.select().where(inventory_table.c.id == item_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Item not found")
    updates = {k: v for k, v in item.model_dump().items() if v is not None}
    if updates:
        await database.execute(inventory_table.update().where(inventory_table.c.id == item_id).values(**updates))
    row = await database.fetch_one(inventory_table.select().where(inventory_table.c.id == item_id))
    return dict(row)


@router.delete("/{item_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_item(item_id: int):
    existing = await database.fetch_one(inventory_table.select().where(inventory_table.c.id == item_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Item not found")
    await database.execute(inventory_table.delete().where(inventory_table.c.id == item_id))
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
pytest tests/test_inventory.py -v
```

Expected: All 5 tests PASS.

- [ ] **Step 7: Commit**

```bash
git add .
git commit -m "feat: inventory CRUD API with expiration query"
```

---

## Task 3: Shopping List API + Threshold Service

**Files:**
- Create: `services/shopping_service.py`
- Create: `routers/shopping.py`
- Create: `tests/test_shopping.py`

- [ ] **Step 1: Write failing shopping tests**

`tests/test_shopping.py`:
```python
import pytest


@pytest.mark.asyncio
async def test_add_manual_shopping_item(client):
    response = await client.post("/api/shopping/", json={
        "name": "Olive Oil",
        "quantity_needed": 1.0,
        "unit": "bottle",
        "source": "manual",
    })
    assert response.status_code == 201
    data = response.json()
    assert data["name"] == "Olive Oil"
    assert data["checked"] is False


@pytest.mark.asyncio
async def test_list_shopping_items(client):
    await client.post("/api/shopping/", json={"name": "Salt", "quantity_needed": 1.0, "unit": "box"})
    response = await client.get("/api/shopping/")
    assert response.status_code == 200
    items = response.json()
    assert any(i["name"] == "Salt" for i in items)


@pytest.mark.asyncio
async def test_check_shopping_item(client):
    create = await client.post("/api/shopping/", json={"name": "Pepper", "quantity_needed": 1.0, "unit": "jar"})
    item_id = create.json()["id"]
    response = await client.patch(f"/api/shopping/{item_id}", json={"checked": True})
    assert response.status_code == 200
    assert response.json()["checked"] is True


@pytest.mark.asyncio
async def test_delete_shopping_item(client):
    create = await client.post("/api/shopping/", json={"name": "ToRemove", "quantity_needed": 1.0, "unit": ""})
    item_id = create.json()["id"]
    response = await client.delete(f"/api/shopping/{item_id}")
    assert response.status_code == 204


@pytest.mark.asyncio
async def test_clear_checked_items(client):
    c1 = await client.post("/api/shopping/", json={"name": "Item A", "quantity_needed": 1.0, "unit": ""})
    c2 = await client.post("/api/shopping/", json={"name": "Item B", "quantity_needed": 1.0, "unit": ""})
    await client.patch(f"/api/shopping/{c1.json()['id']}", json={"checked": True})
    response = await client.delete("/api/shopping/checked")
    assert response.status_code == 200
    remaining = await client.get("/api/shopping/")
    names = [i["name"] for i in remaining.json()]
    assert "Item A" not in names
    assert "Item B" in names


@pytest.mark.asyncio
async def test_generate_from_thresholds(client):
    # Create item below threshold
    item = await client.post("/api/inventory/", json={
        "name": "ThresholdTest",
        "quantity": 0.2,
        "unit": "bottle",
        "location": "pantry",
        "low_threshold": 1.0,
    })
    response = await client.post("/api/shopping/generate-from-thresholds")
    assert response.status_code == 200
    shopping = await client.get("/api/shopping/")
    assert any(i["name"] == "ThresholdTest" for i in shopping.json())
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
pytest tests/test_shopping.py -v
```

Expected: All tests FAIL — routers/shopping.py does not exist.

- [ ] **Step 3: Create services/shopping_service.py**

```python
from database import database, inventory_table, shopping_list_table


async def generate_from_thresholds() -> list[dict]:
    """Find all inventory items below their low_threshold and add to shopping list if not already present."""
    query = inventory_table.select().where(
        inventory_table.c.quantity < inventory_table.c.low_threshold
    )
    low_items = await database.fetch_all(query)
    added = []
    for item in low_items:
        item = dict(item)
        existing = await database.fetch_one(
            shopping_list_table.select().where(
                shopping_list_table.c.inventory_id == item["id"]
            ).where(
                shopping_list_table.c.checked == False
            )
        )
        if not existing:
            insert = shopping_list_table.insert().values(
                inventory_id=item["id"],
                name=item["name"],
                quantity_needed=item["low_threshold"] - item["quantity"],
                unit=item["unit"],
                checked=False,
                source="threshold",
            )
            new_id = await database.execute(insert)
            added.append({**item, "id": new_id, "checked": False, "source": "threshold"})
    return added
```

- [ ] **Step 4: Create routers/shopping.py**

```python
from fastapi import APIRouter, HTTPException, status
from database import database, shopping_list_table
from models import ShoppingListItem, ShoppingListItemCreate, ShoppingListItemUpdate
from services.shopping_service import generate_from_thresholds

router = APIRouter()


@router.post("/", response_model=ShoppingListItem, status_code=status.HTTP_201_CREATED)
async def add_item(item: ShoppingListItemCreate):
    query = shopping_list_table.insert().values(**item.model_dump(), checked=False)
    item_id = await database.execute(query)
    return {**item.model_dump(), "id": item_id, "checked": False}


@router.get("/", response_model=list[ShoppingListItem])
async def list_items(show_checked: bool = False):
    query = shopping_list_table.select()
    if not show_checked:
        query = query.where(shopping_list_table.c.checked == False)
    rows = await database.fetch_all(query)
    return [dict(r) for r in rows]


@router.patch("/{item_id}", response_model=ShoppingListItem)
async def update_item(item_id: int, item: ShoppingListItemUpdate):
    existing = await database.fetch_one(shopping_list_table.select().where(shopping_list_table.c.id == item_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Item not found")
    updates = {k: v for k, v in item.model_dump().items() if v is not None}
    if updates:
        await database.execute(shopping_list_table.update().where(shopping_list_table.c.id == item_id).values(**updates))
    row = await database.fetch_one(shopping_list_table.select().where(shopping_list_table.c.id == item_id))
    return dict(row)


@router.delete("/checked")
async def clear_checked():
    await database.execute(shopping_list_table.delete().where(shopping_list_table.c.checked == True))
    return {"deleted": True}


@router.delete("/{item_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_item(item_id: int):
    existing = await database.fetch_one(shopping_list_table.select().where(shopping_list_table.c.id == item_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Item not found")
    await database.execute(shopping_list_table.delete().where(shopping_list_table.c.id == item_id))


@router.post("/generate-from-thresholds")
async def generate_thresholds():
    added = await generate_from_thresholds()
    return {"added": len(added), "items": added}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
pytest tests/test_shopping.py -v
```

Expected: All 6 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "feat: shopping list API with threshold-based auto-generation"
```

---

## Task 4: Recipe API

**Files:**
- Create: `routers/recipes.py`
- Create: `tests/test_recipes.py`

- [ ] **Step 1: Write failing recipe tests**

`tests/test_recipes.py`:
```python
import pytest


@pytest.mark.asyncio
async def test_create_recipe(client):
    response = await client.post("/api/recipes/", json={
        "name": "Scrambled Eggs",
        "description": "Simple breakfast",
        "instructions": "Beat eggs, cook in pan.",
        "tags": "breakfast,quick",
        "servings": 2,
        "ingredients": [
            {"name": "Eggs", "quantity": 3, "unit": "count"},
            {"name": "Butter", "quantity": 1, "unit": "tbsp"},
        ]
    })
    assert response.status_code == 201
    data = response.json()
    assert data["name"] == "Scrambled Eggs"
    assert len(data["ingredients"]) == 2


@pytest.mark.asyncio
async def test_list_recipes(client):
    await client.post("/api/recipes/", json={"name": "Toast", "servings": 1, "ingredients": []})
    response = await client.get("/api/recipes/")
    assert response.status_code == 200
    assert any(r["name"] == "Toast" for r in response.json())


@pytest.mark.asyncio
async def test_filter_recipes_by_tag(client):
    await client.post("/api/recipes/", json={
        "name": "Pancakes",
        "tags": "breakfast,sweet",
        "servings": 4,
        "ingredients": []
    })
    response = await client.get("/api/recipes/?tag=breakfast")
    assert response.status_code == 200
    names = [r["name"] for r in response.json()]
    assert "Pancakes" in names


@pytest.mark.asyncio
async def test_get_recipe_by_id(client):
    create = await client.post("/api/recipes/", json={"name": "Oatmeal", "servings": 1, "ingredients": []})
    recipe_id = create.json()["id"]
    response = await client.get(f"/api/recipes/{recipe_id}")
    assert response.status_code == 200
    assert response.json()["name"] == "Oatmeal"


@pytest.mark.asyncio
async def test_delete_recipe(client):
    create = await client.post("/api/recipes/", json={"name": "ToDelete", "servings": 1, "ingredients": []})
    recipe_id = create.json()["id"]
    response = await client.delete(f"/api/recipes/{recipe_id}")
    assert response.status_code == 204
    get = await client.get(f"/api/recipes/{recipe_id}")
    assert get.status_code == 404


@pytest.mark.asyncio
async def test_add_recipe_to_shopping_list(client):
    # Create inventory item so ingredient can link
    inv = await client.post("/api/inventory/", json={
        "name": "RecipeEgg",
        "quantity": 0,
        "unit": "count",
        "location": "fridge",
    })
    inv_id = inv.json()["id"]
    recipe = await client.post("/api/recipes/", json={
        "name": "QuickOmelet",
        "servings": 1,
        "ingredients": [{"name": "RecipeEgg", "quantity": 2, "unit": "count", "inventory_id": inv_id}]
    })
    recipe_id = recipe.json()["id"]
    response = await client.post(f"/api/recipes/{recipe_id}/add-to-shopping-list")
    assert response.status_code == 200
    shopping = await client.get("/api/shopping/")
    assert any(i["name"] == "RecipeEgg" for i in shopping.json())


@pytest.mark.asyncio
async def test_filter_by_available_ingredients(client):
    """Recipes where we have all or most ingredients should be returned."""
    inv = await client.post("/api/inventory/", json={
        "name": "AvailableItem",
        "quantity": 5,
        "unit": "count",
        "location": "pantry",
    })
    inv_id = inv.json()["id"]
    await client.post("/api/recipes/", json={
        "name": "MakeableRecipe",
        "servings": 1,
        "ingredients": [{"name": "AvailableItem", "quantity": 1, "unit": "count", "inventory_id": inv_id}]
    })
    response = await client.get("/api/recipes/?available_only=true")
    assert response.status_code == 200
    names = [r["name"] for r in response.json()]
    assert "MakeableRecipe" in names
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
pytest tests/test_recipes.py -v
```

Expected: All tests FAIL — routers/recipes.py does not exist.

- [ ] **Step 3: Create routers/recipes.py**

```python
from fastapi import APIRouter, HTTPException, status
from database import database, recipes_table, recipe_ingredients_table, shopping_list_table, inventory_table
from models import Recipe, RecipeCreate, RecipeUpdate, RecipeIngredientCreate

router = APIRouter()


async def _get_recipe_with_ingredients(recipe_id: int) -> dict | None:
    row = await database.fetch_one(recipes_table.select().where(recipes_table.c.id == recipe_id))
    if not row:
        return None
    ingredients = await database.fetch_all(
        recipe_ingredients_table.select().where(recipe_ingredients_table.c.recipe_id == recipe_id)
    )
    return {**dict(row), "ingredients": [dict(i) for i in ingredients]}


@router.post("/", response_model=Recipe, status_code=status.HTTP_201_CREATED)
async def create_recipe(recipe: RecipeCreate):
    ingredients = recipe.ingredients
    recipe_data = recipe.model_dump(exclude={"ingredients"})
    recipe_id = await database.execute(recipes_table.insert().values(**recipe_data))
    for ing in ingredients:
        await database.execute(recipe_ingredients_table.insert().values(
            recipe_id=recipe_id, **ing.model_dump()
        ))
    return await _get_recipe_with_ingredients(recipe_id)


@router.get("/", response_model=list[Recipe])
async def list_recipes(tag: str = None, available_only: bool = False):
    rows = await database.fetch_all(recipes_table.select())
    result = []
    for row in rows:
        row = dict(row)
        if tag and (not row["tags"] or tag not in row["tags"].split(",")):
            continue
        ingredients = await database.fetch_all(
            recipe_ingredients_table.select().where(recipe_ingredients_table.c.recipe_id == row["id"])
        )
        ingredients = [dict(i) for i in ingredients]
        if available_only:
            all_available = True
            for ing in ingredients:
                if ing["inventory_id"]:
                    inv = await database.fetch_one(
                        inventory_table.select().where(inventory_table.c.id == ing["inventory_id"])
                    )
                    if not inv or dict(inv)["quantity"] < ing["quantity"]:
                        all_available = False
                        break
            if not all_available:
                continue
        result.append({**row, "ingredients": ingredients})
    return result


@router.get("/{recipe_id}", response_model=Recipe)
async def get_recipe(recipe_id: int):
    recipe = await _get_recipe_with_ingredients(recipe_id)
    if not recipe:
        raise HTTPException(status_code=404, detail="Recipe not found")
    return recipe


@router.patch("/{recipe_id}", response_model=Recipe)
async def update_recipe(recipe_id: int, recipe: RecipeUpdate):
    existing = await database.fetch_one(recipes_table.select().where(recipes_table.c.id == recipe_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Recipe not found")
    updates = {k: v for k, v in recipe.model_dump().items() if v is not None}
    if updates:
        await database.execute(recipes_table.update().where(recipes_table.c.id == recipe_id).values(**updates))
    return await _get_recipe_with_ingredients(recipe_id)


@router.delete("/{recipe_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_recipe(recipe_id: int):
    existing = await database.fetch_one(recipes_table.select().where(recipes_table.c.id == recipe_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Recipe not found")
    await database.execute(recipe_ingredients_table.delete().where(recipe_ingredients_table.c.recipe_id == recipe_id))
    await database.execute(recipes_table.delete().where(recipes_table.c.id == recipe_id))


@router.post("/{recipe_id}/add-to-shopping-list")
async def add_recipe_to_shopping_list(recipe_id: int, servings: int = 1):
    recipe = await _get_recipe_with_ingredients(recipe_id)
    if not recipe:
        raise HTTPException(status_code=404, detail="Recipe not found")
    scale = servings / max(recipe["servings"], 1)
    added = []
    for ing in recipe["ingredients"]:
        needed = ing["quantity"] * scale
        # Check how much we already have
        have = 0.0
        if ing["inventory_id"]:
            inv = await database.fetch_one(inventory_table.select().where(inventory_table.c.id == ing["inventory_id"]))
            if inv:
                have = dict(inv)["quantity"]
        shortfall = needed - have
        if shortfall > 0:
            new_id = await database.execute(shopping_list_table.insert().values(
                inventory_id=ing["inventory_id"],
                name=ing["name"],
                quantity_needed=shortfall,
                unit=ing["unit"],
                checked=False,
                source="recipe",
            ))
            added.append({"id": new_id, "name": ing["name"], "quantity_needed": shortfall})
    return {"added": len(added), "items": added}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
pytest tests/test_recipes.py -v
```

Expected: All 7 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: recipe CRUD with ingredient management, tag filtering, and shopping list integration"
```

---

## Task 5: Meal Calendar API + Weekly Shopping Service

**Files:**
- Create: `services/calendar_service.py`
- Create: `routers/calendar.py`
- Create: `tests/test_calendar.py`

- [ ] **Step 1: Write failing calendar tests**

`tests/test_calendar.py`:
```python
import pytest


@pytest.mark.asyncio
async def test_add_meal_to_calendar(client):
    recipe = await client.post("/api/recipes/", json={"name": "CalRecipe", "servings": 2, "ingredients": []})
    recipe_id = recipe.json()["id"]
    response = await client.post("/api/calendar/", json={
        "date": "2026-04-07",
        "meal_slot": "dinner",
        "recipe_id": recipe_id,
        "servings": 2,
    })
    assert response.status_code == 201
    data = response.json()
    assert data["recipe_id"] == recipe_id


@pytest.mark.asyncio
async def test_get_week_calendar(client):
    recipe = await client.post("/api/recipes/", json={"name": "WeekRecipe", "servings": 1, "ingredients": []})
    recipe_id = recipe.json()["id"]
    await client.post("/api/calendar/", json={"date": "2026-04-08", "meal_slot": "lunch", "recipe_id": recipe_id, "servings": 1})
    response = await client.get("/api/calendar/week?start=2026-04-07")
    assert response.status_code == 200
    entries = response.json()
    assert any(e["recipe_id"] == recipe_id for e in entries)


@pytest.mark.asyncio
async def test_delete_calendar_entry(client):
    recipe = await client.post("/api/recipes/", json={"name": "DelRecipe", "servings": 1, "ingredients": []})
    recipe_id = recipe.json()["id"]
    entry = await client.post("/api/calendar/", json={"date": "2026-04-09", "meal_slot": "breakfast", "recipe_id": recipe_id, "servings": 1})
    entry_id = entry.json()["id"]
    response = await client.delete(f"/api/calendar/{entry_id}")
    assert response.status_code == 204


@pytest.mark.asyncio
async def test_weekly_shopping_list_accounts_for_inventory(client):
    """
    Inventory has 2 eggs. Calendar has a recipe needing 4 eggs Mon and 4 eggs Wed.
    Monday uses 4 eggs (need to buy 2 more since we only have 2).
    Wednesday uses 4 eggs (need to buy 4 more since Monday depleted stock to 0 + we bought 2).
    Total shortfall = 6 eggs.
    """
    inv = await client.post("/api/inventory/", json={
        "name": "CalEgg",
        "quantity": 2.0,
        "unit": "count",
        "location": "fridge",
    })
    inv_id = inv.json()["id"]
    recipe = await client.post("/api/recipes/", json={
        "name": "EggDish",
        "servings": 1,
        "ingredients": [{"name": "CalEgg", "quantity": 4, "unit": "count", "inventory_id": inv_id}]
    })
    recipe_id = recipe.json()["id"]
    await client.post("/api/calendar/", json={"date": "2026-04-14", "meal_slot": "dinner", "recipe_id": recipe_id, "servings": 1})
    await client.post("/api/calendar/", json={"date": "2026-04-16", "meal_slot": "dinner", "recipe_id": recipe_id, "servings": 1})
    response = await client.post("/api/calendar/generate-weekly-shopping?start=2026-04-14")
    assert response.status_code == 200
    data = response.json()
    egg_items = [i for i in data["items"] if i["name"] == "CalEgg"]
    assert len(egg_items) > 0
    total_needed = sum(i["quantity_needed"] for i in egg_items)
    assert total_needed == 6.0
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
pytest tests/test_calendar.py -v
```

Expected: All tests FAIL — routers/calendar.py does not exist.

- [ ] **Step 3: Create services/calendar_service.py**

```python
from datetime import date, timedelta
from database import database, meal_calendar_table, recipe_ingredients_table, inventory_table, shopping_list_table, recipes_table


async def generate_weekly_shopping(week_start: str) -> list[dict]:
    """
    For a 7-day week starting at week_start:
    1. Get all calendar entries sorted by date.
    2. Simulate daily consumption against current inventory.
    3. For each day's recipes, compute shortfall = max(0, needed - available).
    4. Add shortfall items to shopping list (source="calendar").
    5. After computing shortfall, deduct what we'll have (shortfall + existing stock) from simulated inventory.
    """
    start = date.fromisoformat(week_start)
    end = start + timedelta(days=6)

    # Load all calendar entries for the week, sorted by date
    entries = await database.fetch_all(
        meal_calendar_table.select().where(
            meal_calendar_table.c.date >= week_start
        ).where(
            meal_calendar_table.c.date <= end.isoformat()
        ).order_by(meal_calendar_table.c.date)
    )

    # Build simulated inventory: {inventory_id: quantity}
    all_inv = await database.fetch_all(inventory_table.select())
    simulated = {dict(r)["id"]: dict(r)["quantity"] for r in all_inv}

    # Track shopping needs: {(inventory_id_or_name, unit): quantity_needed}
    shopping_needed: dict[tuple, dict] = {}

    for entry in entries:
        entry = dict(entry)
        recipe_row = await database.fetch_one(recipes_table.select().where(recipes_table.c.id == entry["recipe_id"]))
        if not recipe_row:
            continue
        recipe = dict(recipe_row)
        scale = entry["servings"] / max(recipe["servings"], 1)

        ingredients = await database.fetch_all(
            recipe_ingredients_table.select().where(
                recipe_ingredients_table.c.recipe_id == entry["recipe_id"]
            )
        )
        for ing in ingredients:
            ing = dict(ing)
            needed = ing["quantity"] * scale
            inv_id = ing["inventory_id"]
            available = simulated.get(inv_id, 0.0) if inv_id else 0.0
            shortfall = max(0.0, needed - available)

            key = (inv_id, ing["name"], ing["unit"])
            if shortfall > 0:
                if key not in shopping_needed:
                    shopping_needed[key] = {
                        "inventory_id": inv_id,
                        "name": ing["name"],
                        "unit": ing["unit"],
                        "quantity_needed": 0.0,
                    }
                shopping_needed[key]["quantity_needed"] += shortfall

            # Deduct from simulated inventory for future days
            if inv_id:
                simulated[inv_id] = max(0.0, available - needed)

    # Write shopping list items
    added = []
    for item_data in shopping_needed.values():
        # Avoid duplicate calendar items
        existing = await database.fetch_one(
            shopping_list_table.select().where(
                shopping_list_table.c.name == item_data["name"]
            ).where(
                shopping_list_table.c.source == "calendar"
            ).where(
                shopping_list_table.c.checked == False
            )
        )
        if existing:
            await database.execute(
                shopping_list_table.update().where(
                    shopping_list_table.c.id == dict(existing)["id"]
                ).values(quantity_needed=dict(existing)["quantity_needed"] + item_data["quantity_needed"])
            )
            added.append({**item_data, "id": dict(existing)["id"], "checked": False, "source": "calendar"})
        else:
            new_id = await database.execute(shopping_list_table.insert().values(
                **item_data, checked=False, source="calendar"
            ))
            added.append({**item_data, "id": new_id, "checked": False, "source": "calendar"})
    return added
```

- [ ] **Step 4: Create routers/calendar.py**

```python
from fastapi import APIRouter, HTTPException, status
from datetime import date, timedelta
from database import database, meal_calendar_table, recipes_table
from models import MealCalendarEntry, MealCalendarEntryCreate
from services.calendar_service import generate_weekly_shopping

router = APIRouter()


@router.post("/", response_model=MealCalendarEntry, status_code=status.HTTP_201_CREATED)
async def add_meal(entry: MealCalendarEntryCreate):
    recipe = await database.fetch_one(recipes_table.select().where(recipes_table.c.id == entry.recipe_id))
    if not recipe:
        raise HTTPException(status_code=404, detail="Recipe not found")
    entry_id = await database.execute(meal_calendar_table.insert().values(**entry.model_dump()))
    return {**entry.model_dump(), "id": entry_id}


@router.get("/week", response_model=list[MealCalendarEntry])
async def get_week(start: str = None):
    if not start:
        start = date.today().isoformat()
    end = (date.fromisoformat(start) + timedelta(days=6)).isoformat()
    query = meal_calendar_table.select().where(
        meal_calendar_table.c.date >= start
    ).where(
        meal_calendar_table.c.date <= end
    ).order_by(meal_calendar_table.c.date)
    rows = await database.fetch_all(query)
    return [dict(r) for r in rows]


@router.delete("/{entry_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_entry(entry_id: int):
    existing = await database.fetch_one(meal_calendar_table.select().where(meal_calendar_table.c.id == entry_id))
    if not existing:
        raise HTTPException(status_code=404, detail="Entry not found")
    await database.execute(meal_calendar_table.delete().where(meal_calendar_table.c.id == entry_id))


@router.post("/generate-weekly-shopping")
async def create_weekly_shopping(start: str = None):
    if not start:
        start = date.today().isoformat()
    items = await generate_weekly_shopping(start)
    return {"week_start": start, "items": items}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
pytest tests/test_calendar.py -v
```

Expected: All 4 tests PASS.

- [ ] **Step 6: Run all tests to verify nothing regressed**

```bash
pytest -v
```

Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add .
git commit -m "feat: meal calendar API with weekly shopping list generation that tracks simulated inventory depletion"
```

---

## Task 6: Single-Page Frontend (SPA)

**Files:**
- Modify: `static/index.html` (full rewrite)

This task builds the complete UI. It uses Alpine.js for reactivity and Tailwind CSS, both via CDN — no build step. The UI has a bottom tab bar with 4 sections: Inventory, Shopping, Recipes, Calendar.

- [ ] **Step 1: Write static/index.html**

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Kitchen Manager</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
  <style>
    [x-cloak] { display: none !important; }
    .tab-bar { padding-bottom: env(safe-area-inset-bottom); }
  </style>
</head>
<body class="bg-gray-50 min-h-screen" x-data="app()" x-cloak>

  <!-- Header -->
  <header class="bg-green-700 text-white px-4 py-3 flex items-center justify-between sticky top-0 z-10">
    <h1 class="text-lg font-bold">🍽 Kitchen Manager</h1>
    <span class="text-sm opacity-75" x-text="activeTab"></span>
  </header>

  <!-- Main content -->
  <main class="pb-20 max-w-2xl mx-auto px-4">

    <!-- ===== INVENTORY TAB ===== -->
    <section x-show="activeTab === 'inventory'">
      <div class="flex gap-2 my-4">
        <input x-model="inventorySearch" @input.debounce.300ms="fetchInventory()" type="text" placeholder="Search items..." class="flex-1 border rounded-lg px-3 py-2 text-sm" />
        <select x-model="inventoryLocation" @change="fetchInventory()" class="border rounded-lg px-3 py-2 text-sm">
          <option value="">All Locations</option>
          <template x-for="loc in locations">
            <option :value="loc" x-text="loc"></option>
          </template>
        </select>
        <button @click="openAddItem()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
      </div>

      <!-- Expiring soon banner -->
      <template x-if="expiringItems.length > 0">
        <div class="bg-yellow-50 border border-yellow-200 rounded-lg p-3 mb-4 text-sm text-yellow-800">
          ⚠️ <strong x-text="expiringItems.length"></strong> items expiring within 7 days
        </div>
      </template>

      <!-- Item list -->
      <div class="space-y-2">
        <template x-for="item in inventoryItems" :key="item.id">
          <div class="bg-white rounded-xl shadow-sm border p-3 flex items-start justify-between">
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2">
                <span class="font-medium text-gray-900 truncate" x-text="item.name"></span>
                <template x-if="item.quantity <= item.low_threshold">
                  <span class="text-xs bg-red-100 text-red-700 px-1.5 py-0.5 rounded">Low</span>
                </template>
              </div>
              <div class="text-sm text-gray-500 mt-0.5">
                <span x-text="item.quantity"></span> <span x-text="item.unit"></span>
                &bull; <span x-text="item.location || 'No location'"></span>
                <template x-if="item.expiration_date">
                  <span> &bull; Exp: <span x-text="item.expiration_date"></span></span>
                </template>
              </div>
            </div>
            <div class="flex gap-2 ml-2">
              <button @click="editItem(item)" class="text-blue-600 text-sm font-medium">Edit</button>
              <button @click="deleteItem(item.id)" class="text-red-500 text-sm">✕</button>
            </div>
          </div>
        </template>
        <template x-if="inventoryItems.length === 0">
          <p class="text-center text-gray-400 py-8">No items found. Add some!</p>
        </template>
      </div>
    </section>

    <!-- ===== SHOPPING TAB ===== -->
    <section x-show="activeTab === 'shopping'">
      <div class="flex gap-2 my-4">
        <button @click="generateFromThresholds()" class="flex-1 bg-blue-600 text-white rounded-lg px-3 py-2 text-sm font-medium">
          ↻ Auto-generate from low stock
        </button>
        <button @click="openAddShoppingItem()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
      </div>
      <button @click="clearCheckedItems()" class="mb-4 text-sm text-red-500 underline w-full text-center">
        Clear checked items
      </button>

      <div class="space-y-2">
        <template x-for="item in shoppingItems" :key="item.id">
          <div class="bg-white rounded-xl shadow-sm border p-3 flex items-center gap-3"
               :class="item.checked ? 'opacity-50' : ''">
            <input type="checkbox" :checked="item.checked"
                   @change="toggleShoppingItem(item)"
                   class="w-5 h-5 accent-green-600 flex-shrink-0" />
            <div class="flex-1 min-w-0">
              <span class="font-medium" :class="item.checked ? 'line-through' : ''" x-text="item.name"></span>
              <div class="text-sm text-gray-500">
                <span x-text="item.quantity_needed"></span> <span x-text="item.unit"></span>
                <span class="ml-1 text-xs text-gray-400" x-text="'(' + item.source + ')'"></span>
              </div>
            </div>
            <button @click="deleteShoppingItem(item.id)" class="text-red-400 text-sm">✕</button>
          </div>
        </template>
        <template x-if="shoppingItems.length === 0">
          <p class="text-center text-gray-400 py-8">Shopping list is empty!</p>
        </template>
      </div>
    </section>

    <!-- ===== RECIPES TAB ===== -->
    <section x-show="activeTab === 'recipes'">
      <div class="flex gap-2 my-4">
        <input x-model="recipeTagFilter" @input.debounce.300ms="fetchRecipes()" type="text" placeholder="Filter by tag..." class="flex-1 border rounded-lg px-3 py-2 text-sm" />
        <label class="flex items-center gap-1 text-sm text-gray-600 whitespace-nowrap">
          <input type="checkbox" x-model="recipeAvailableOnly" @change="fetchRecipes()" class="accent-green-600" />
          Have ingredients
        </label>
        <button @click="openAddRecipe()" class="bg-green-600 text-white rounded-lg px-3 py-2 text-sm font-medium">+ Add</button>
      </div>

      <div class="space-y-3">
        <template x-for="recipe in recipes" :key="recipe.id">
          <div class="bg-white rounded-xl shadow-sm border p-4">
            <div class="flex items-start justify-between">
              <div>
                <h3 class="font-semibold text-gray-900" x-text="recipe.name"></h3>
                <p class="text-sm text-gray-500 mt-0.5" x-text="recipe.description || ''"></p>
                <template x-if="recipe.tags">
                  <div class="flex flex-wrap gap-1 mt-1">
                    <template x-for="tag in (recipe.tags || '').split(',')" :key="tag">
                      <span class="text-xs bg-green-100 text-green-700 px-1.5 py-0.5 rounded" x-text="tag.trim()"></span>
                    </template>
                  </div>
                </template>
              </div>
              <div class="flex gap-2 ml-2">
                <button @click="viewRecipe(recipe)" class="text-blue-600 text-sm">View</button>
                <button @click="deleteRecipe(recipe.id)" class="text-red-400 text-sm">✕</button>
              </div>
            </div>
          </div>
        </template>
        <template x-if="recipes.length === 0">
          <p class="text-center text-gray-400 py-8">No recipes found.</p>
        </template>
      </div>
    </section>

    <!-- ===== CALENDAR TAB ===== -->
    <section x-show="activeTab === 'calendar'">
      <div class="flex items-center gap-2 my-4">
        <button @click="prevWeek()" class="bg-white border rounded-lg px-3 py-2 text-sm">←</button>
        <span class="flex-1 text-center text-sm font-medium text-gray-700"
              x-text="calendarWeekLabel"></span>
        <button @click="nextWeek()" class="bg-white border rounded-lg px-3 py-2 text-sm">→</button>
      </div>
      <button @click="generateWeeklyShopping()" class="mb-4 w-full bg-blue-600 text-white rounded-lg px-3 py-2 text-sm font-medium">
        ↻ Generate weekly shopping list
      </button>

      <div class="space-y-3">
        <template x-for="day in calendarDays" :key="day.date">
          <div class="bg-white rounded-xl shadow-sm border overflow-hidden">
            <div class="bg-gray-100 px-3 py-2 text-sm font-semibold text-gray-700"
                 x-text="day.label"></div>
            <div class="p-3 space-y-2">
              <template x-for="slot in ['breakfast','lunch','dinner']" :key="slot">
                <div class="flex items-center gap-2">
                  <span class="text-xs text-gray-400 w-16 capitalize" x-text="slot"></span>
                  <template x-if="getCalendarEntry(day.date, slot)">
                    <div class="flex-1 flex items-center justify-between bg-green-50 rounded-lg px-2 py-1">
                      <span class="text-sm text-green-800"
                            x-text="getRecipeName(getCalendarEntry(day.date, slot).recipe_id)"></span>
                      <button @click="deleteCalendarEntry(getCalendarEntry(day.date, slot).id)"
                              class="text-red-400 text-xs ml-2">✕</button>
                    </div>
                  </template>
                  <template x-if="!getCalendarEntry(day.date, slot)">
                    <button @click="openAddCalendarEntry(day.date, slot)"
                            class="flex-1 border border-dashed border-gray-300 rounded-lg px-2 py-1 text-xs text-gray-400 text-left hover:border-green-400">
                      + Add meal
                    </button>
                  </template>
                </div>
              </template>
            </div>
          </div>
        </template>
      </div>
    </section>

  </main>

  <!-- Tab Bar -->
  <nav class="tab-bar fixed bottom-0 left-0 right-0 bg-white border-t flex justify-around py-2 z-10">
    <template x-for="tab in tabs" :key="tab.id">
      <button @click="switchTab(tab.id)"
              class="flex flex-col items-center gap-0.5 px-4 py-1"
              :class="activeTab === tab.id ? 'text-green-600' : 'text-gray-400'">
        <span class="text-xl" x-text="tab.icon"></span>
        <span class="text-xs font-medium" x-text="tab.label"></span>
      </button>
    </template>
  </nav>

  <!-- ===== MODALS ===== -->

  <!-- Add/Edit Inventory Item Modal -->
  <div x-show="modal === 'item'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
    <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-4 max-h-[90vh] overflow-y-auto">
      <h2 class="text-lg font-bold" x-text="editingItem ? 'Edit Item' : 'Add Item'"></h2>
      <input x-model="itemForm.name" placeholder="Name *" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <div class="flex gap-2">
        <input x-model.number="itemForm.quantity" type="number" placeholder="Quantity" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
        <input x-model="itemForm.unit" placeholder="Unit (e.g. kg)" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
      </div>
      <input x-model="itemForm.location" placeholder="Location (fridge, pantry...)" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <input x-model="itemForm.expiration_date" type="date" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <div class="flex gap-2">
        <label class="text-sm text-gray-600 self-center">Low threshold:</label>
        <input x-model.number="itemForm.low_threshold" type="number" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
      </div>
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveItem()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
    </div>
  </div>

  <!-- Add Shopping Item Modal -->
  <div x-show="modal === 'shopping'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
    <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-4">
      <h2 class="text-lg font-bold">Add Shopping Item</h2>
      <input x-model="shoppingForm.name" placeholder="Item name *" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <div class="flex gap-2">
        <input x-model.number="shoppingForm.quantity_needed" type="number" placeholder="Qty" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
        <input x-model="shoppingForm.unit" placeholder="Unit" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
      </div>
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveShoppingItem()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
    </div>
  </div>

  <!-- Add Recipe Modal -->
  <div x-show="modal === 'recipe'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
    <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-4 max-h-[90vh] overflow-y-auto">
      <h2 class="text-lg font-bold">Add Recipe</h2>
      <input x-model="recipeForm.name" placeholder="Recipe name *" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <input x-model="recipeForm.description" placeholder="Short description" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <textarea x-model="recipeForm.instructions" placeholder="Instructions" rows="3" class="w-full border rounded-lg px-3 py-2 text-sm"></textarea>
      <input x-model="recipeForm.tags" placeholder="Tags (comma-separated: breakfast,quick)" class="w-full border rounded-lg px-3 py-2 text-sm" />
      <input x-model.number="recipeForm.servings" type="number" placeholder="Servings" class="w-full border rounded-lg px-3 py-2 text-sm" />

      <h3 class="font-semibold text-sm text-gray-700">Ingredients</h3>
      <template x-for="(ing, idx) in recipeForm.ingredients" :key="idx">
        <div class="flex gap-1">
          <input x-model="ing.name" placeholder="Ingredient" class="flex-1 border rounded-lg px-2 py-1.5 text-sm" />
          <input x-model.number="ing.quantity" type="number" placeholder="Qty" class="w-16 border rounded-lg px-2 py-1.5 text-sm" />
          <input x-model="ing.unit" placeholder="Unit" class="w-16 border rounded-lg px-2 py-1.5 text-sm" />
          <button @click="recipeForm.ingredients.splice(idx, 1)" class="text-red-400 text-sm px-1">✕</button>
        </div>
      </template>
      <button @click="recipeForm.ingredients.push({name:'',quantity:1,unit:'',inventory_id:null})"
              class="text-sm text-blue-600 underline">+ Add ingredient</button>

      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveRecipe()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
    </div>
  </div>

  <!-- View Recipe Modal -->
  <div x-show="modal === 'viewRecipe'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
    <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-3 max-h-[90vh] overflow-y-auto" x-show="selectedRecipe">
      <h2 class="text-lg font-bold" x-text="selectedRecipe?.name"></h2>
      <p class="text-sm text-gray-500" x-text="selectedRecipe?.description"></p>
      <template x-if="selectedRecipe?.tags">
        <div class="flex flex-wrap gap-1">
          <template x-for="tag in (selectedRecipe?.tags || '').split(',')" :key="tag">
            <span class="text-xs bg-green-100 text-green-700 px-1.5 py-0.5 rounded" x-text="tag.trim()"></span>
          </template>
        </div>
      </template>
      <div>
        <h3 class="font-semibold text-sm mb-1">Ingredients</h3>
        <ul class="space-y-1">
          <template x-for="ing in selectedRecipe?.ingredients" :key="ing.id">
            <li class="text-sm text-gray-700">
              • <span x-text="ing.quantity"></span> <span x-text="ing.unit"></span> <span x-text="ing.name"></span>
            </li>
          </template>
        </ul>
      </div>
      <template x-if="selectedRecipe?.instructions">
        <div>
          <h3 class="font-semibold text-sm mb-1">Instructions</h3>
          <p class="text-sm text-gray-700 whitespace-pre-wrap" x-text="selectedRecipe?.instructions"></p>
        </div>
      </template>
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Close</button>
        <button @click="addRecipeToShoppingList(selectedRecipe)" class="flex-1 bg-blue-600 text-white rounded-lg py-2 text-sm font-medium">
          + Missing items to shopping list
        </button>
      </div>
    </div>
  </div>

  <!-- Add Calendar Entry Modal -->
  <div x-show="modal === 'calendarEntry'" class="fixed inset-0 bg-black/50 z-20 flex items-end justify-center sm:items-center">
    <div class="bg-white rounded-t-2xl sm:rounded-2xl w-full max-w-lg p-6 space-y-4">
      <h2 class="text-lg font-bold">Add Meal</h2>
      <p class="text-sm text-gray-500">
        <span x-text="calendarEntryForm.date"></span> — <span x-text="calendarEntryForm.meal_slot" class="capitalize"></span>
      </p>
      <select x-model.number="calendarEntryForm.recipe_id" class="w-full border rounded-lg px-3 py-2 text-sm">
        <option value="">Select a recipe...</option>
        <template x-for="recipe in recipes" :key="recipe.id">
          <option :value="recipe.id" x-text="recipe.name"></option>
        </template>
      </select>
      <div class="flex gap-2 items-center">
        <label class="text-sm text-gray-600">Servings:</label>
        <input x-model.number="calendarEntryForm.servings" type="number" min="1" class="flex-1 border rounded-lg px-3 py-2 text-sm" />
      </div>
      <div class="flex gap-2 pt-2">
        <button @click="modal = null" class="flex-1 border rounded-lg py-2 text-sm">Cancel</button>
        <button @click="saveCalendarEntry()" class="flex-1 bg-green-600 text-white rounded-lg py-2 text-sm font-medium">Save</button>
      </div>
    </div>
  </div>

  <!-- Toast notification -->
  <div x-show="toast" x-transition
       class="fixed top-16 left-1/2 -translate-x-1/2 bg-gray-900 text-white text-sm px-4 py-2 rounded-full z-30 shadow-lg"
       x-text="toast"></div>

  <script>
    function app() {
      return {
        // State
        activeTab: 'inventory',
        tabs: [
          { id: 'inventory', label: 'Pantry', icon: '🥫' },
          { id: 'shopping', label: 'Shopping', icon: '🛒' },
          { id: 'recipes', label: 'Recipes', icon: '📖' },
          { id: 'calendar', label: 'Calendar', icon: '📅' },
        ],
        modal: null,
        toast: '',

        // Inventory
        inventoryItems: [],
        expiringItems: [],
        inventorySearch: '',
        inventoryLocation: '',
        locations: ['fridge', 'freezer', 'pantry', 'cabinet', 'other'],
        editingItem: null,
        itemForm: { name: '', quantity: 0, unit: '', location: '', expiration_date: '', low_threshold: 1 },

        // Shopping
        shoppingItems: [],
        shoppingForm: { name: '', quantity_needed: 1, unit: '' },

        // Recipes
        recipes: [],
        recipeTagFilter: '',
        recipeAvailableOnly: false,
        selectedRecipe: null,
        recipeForm: { name: '', description: '', instructions: '', tags: '', servings: 2, ingredients: [] },

        // Calendar
        calendarEntries: [],
        calendarWeekStart: null,
        calendarEntryForm: { date: '', meal_slot: 'dinner', recipe_id: '', servings: 1 },

        async init() {
          this.calendarWeekStart = this.getMonday(new Date());
          await this.fetchInventory();
          await this.fetchExpiring();
          await this.fetchShoppingList();
          await this.fetchRecipes();
          await this.fetchCalendar();
        },

        async switchTab(tab) {
          this.activeTab = tab;
          if (tab === 'inventory') { await this.fetchInventory(); await this.fetchExpiring(); }
          if (tab === 'shopping') await this.fetchShoppingList();
          if (tab === 'recipes') await this.fetchRecipes();
          if (tab === 'calendar') await this.fetchCalendar();
        },

        showToast(msg) {
          this.toast = msg;
          setTimeout(() => this.toast = '', 2500);
        },

        // --- Inventory ---
        async fetchInventory() {
          const params = new URLSearchParams();
          if (this.inventorySearch) params.set('name', this.inventorySearch);
          if (this.inventoryLocation) params.set('location', this.inventoryLocation);
          const res = await fetch('/api/inventory/?' + params);
          this.inventoryItems = await res.json();
        },

        async fetchExpiring() {
          const res = await fetch('/api/inventory/expiring?days=7');
          this.expiringItems = await res.json();
        },

        openAddItem() {
          this.editingItem = null;
          this.itemForm = { name: '', quantity: 0, unit: '', location: '', expiration_date: '', low_threshold: 1 };
          this.modal = 'item';
        },

        editItem(item) {
          this.editingItem = item;
          this.itemForm = { ...item };
          this.modal = 'item';
        },

        async saveItem() {
          if (!this.itemForm.name) return;
          if (this.editingItem) {
            await fetch(`/api/inventory/${this.editingItem.id}`, {
              method: 'PATCH',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(this.itemForm),
            });
            this.showToast('Item updated');
          } else {
            await fetch('/api/inventory/', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify(this.itemForm),
            });
            this.showToast('Item added');
          }
          this.modal = null;
          await this.fetchInventory();
          await this.fetchExpiring();
        },

        async deleteItem(id) {
          if (!confirm('Delete this item?')) return;
          await fetch(`/api/inventory/${id}`, { method: 'DELETE' });
          await this.fetchInventory();
          this.showToast('Item deleted');
        },

        // --- Shopping ---
        async fetchShoppingList() {
          const res = await fetch('/api/shopping/');
          this.shoppingItems = await res.json();
        },

        openAddShoppingItem() {
          this.shoppingForm = { name: '', quantity_needed: 1, unit: '' };
          this.modal = 'shopping';
        },

        async saveShoppingItem() {
          if (!this.shoppingForm.name) return;
          await fetch('/api/shopping/', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(this.shoppingForm),
          });
          this.modal = null;
          await this.fetchShoppingList();
          this.showToast('Added to list');
        },

        async toggleShoppingItem(item) {
          await fetch(`/api/shopping/${item.id}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ checked: !item.checked }),
          });
          await this.fetchShoppingList();
        },

        async deleteShoppingItem(id) {
          await fetch(`/api/shopping/${id}`, { method: 'DELETE' });
          await this.fetchShoppingList();
        },

        async clearCheckedItems() {
          await fetch('/api/shopping/checked', { method: 'DELETE' });
          await this.fetchShoppingList();
          this.showToast('Cleared checked items');
        },

        async generateFromThresholds() {
          const res = await fetch('/api/shopping/generate-from-thresholds', { method: 'POST' });
          const data = await res.json();
          await this.fetchShoppingList();
          this.showToast(`Added ${data.added} low-stock items`);
        },

        // --- Recipes ---
        async fetchRecipes() {
          const params = new URLSearchParams();
          if (this.recipeTagFilter) params.set('tag', this.recipeTagFilter);
          if (this.recipeAvailableOnly) params.set('available_only', 'true');
          const res = await fetch('/api/recipes/?' + params);
          this.recipes = await res.json();
        },

        openAddRecipe() {
          this.recipeForm = { name: '', description: '', instructions: '', tags: '', servings: 2, ingredients: [] };
          this.modal = 'recipe';
        },

        async saveRecipe() {
          if (!this.recipeForm.name) return;
          await fetch('/api/recipes/', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(this.recipeForm),
          });
          this.modal = null;
          await this.fetchRecipes();
          this.showToast('Recipe saved');
        },

        viewRecipe(recipe) {
          this.selectedRecipe = recipe;
          this.modal = 'viewRecipe';
        },

        async addRecipeToShoppingList(recipe) {
          const res = await fetch(`/api/recipes/${recipe.id}/add-to-shopping-list`, { method: 'POST' });
          const data = await res.json();
          this.modal = null;
          this.showToast(`Added ${data.added} missing ingredient(s) to shopping list`);
          await this.fetchShoppingList();
        },

        async deleteRecipe(id) {
          if (!confirm('Delete this recipe?')) return;
          await fetch(`/api/recipes/${id}`, { method: 'DELETE' });
          await this.fetchRecipes();
          this.showToast('Recipe deleted');
        },

        // --- Calendar ---
        getMonday(d) {
          const day = d.getDay();
          const diff = d.getDate() - day + (day === 0 ? -6 : 1);
          const monday = new Date(d.setDate(diff));
          monday.setHours(0, 0, 0, 0);
          return monday;
        },

        get calendarWeekLabel() {
          if (!this.calendarWeekStart) return '';
          const end = new Date(this.calendarWeekStart);
          end.setDate(end.getDate() + 6);
          return this.calendarWeekStart.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) +
            ' – ' + end.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
        },

        get calendarDays() {
          if (!this.calendarWeekStart) return [];
          const days = [];
          for (let i = 0; i < 7; i++) {
            const d = new Date(this.calendarWeekStart);
            d.setDate(d.getDate() + i);
            days.push({
              date: d.toISOString().split('T')[0],
              label: d.toLocaleDateString('en-US', { weekday: 'long', month: 'short', day: 'numeric' }),
            });
          }
          return days;
        },

        prevWeek() {
          const d = new Date(this.calendarWeekStart);
          d.setDate(d.getDate() - 7);
          this.calendarWeekStart = d;
          this.fetchCalendar();
        },

        nextWeek() {
          const d = new Date(this.calendarWeekStart);
          d.setDate(d.getDate() + 7);
          this.calendarWeekStart = d;
          this.fetchCalendar();
        },

        async fetchCalendar() {
          const start = this.calendarWeekStart.toISOString().split('T')[0];
          const res = await fetch(`/api/calendar/week?start=${start}`);
          this.calendarEntries = await res.json();
        },

        getCalendarEntry(date, slot) {
          return this.calendarEntries.find(e => e.date === date && e.meal_slot === slot) || null;
        },

        getRecipeName(recipeId) {
          const r = this.recipes.find(r => r.id === recipeId);
          return r ? r.name : 'Unknown';
        },

        openAddCalendarEntry(date, slot) {
          this.calendarEntryForm = { date, meal_slot: slot, recipe_id: '', servings: 1 };
          this.modal = 'calendarEntry';
        },

        async saveCalendarEntry() {
          if (!this.calendarEntryForm.recipe_id) return;
          await fetch('/api/calendar/', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(this.calendarEntryForm),
          });
          this.modal = null;
          await this.fetchCalendar();
          this.showToast('Meal added');
        },

        async deleteCalendarEntry(id) {
          await fetch(`/api/calendar/${id}`, { method: 'DELETE' });
          await this.fetchCalendar();
          this.showToast('Meal removed');
        },

        async generateWeeklyShopping() {
          const start = this.calendarWeekStart.toISOString().split('T')[0];
          const res = await fetch(`/api/calendar/generate-weekly-shopping?start=${start}`, { method: 'POST' });
          const data = await res.json();
          this.showToast(`Added ${data.items.length} item(s) to shopping list`);
          await this.fetchShoppingList();
          this.activeTab = 'shopping';
        },
      };
    }
  </script>
</body>
</html>
```

- [ ] **Step 2: Start the app and manually verify the UI loads**

```bash
uvicorn main:app --reload
```

Open `http://localhost:8000` in a browser. Verify:
- 4 tabs visible at the bottom (Pantry, Shopping, Recipes, Calendar)
- Inventory tab shows empty state with "+ Add" button
- Switching tabs works
- Adding an item via modal works

- [ ] **Step 3: Commit**

```bash
git add static/index.html
git commit -m "feat: complete single-page frontend with Alpine.js — inventory, shopping, recipes, and calendar tabs"
```

---

## Task 7: Final Integration Test + Smoke Test

**Files:**
- Create: `tests/test_integration.py`

- [ ] **Step 1: Write integration test that exercises the full flow**

`tests/test_integration.py`:
```python
import pytest


@pytest.mark.asyncio
async def test_full_weekly_planning_flow(client):
    """
    Full end-to-end flow:
    1. Add ingredients to inventory (some low)
    2. Auto-generate shopping list from thresholds
    3. Create a recipe using those ingredients
    4. Add recipe to a week's calendar
    5. Generate weekly shopping list — verify it accounts for existing stock
    6. View shopping list has correct items
    """
    # 1. Add pasta — enough for one meal
    pasta = await client.post("/api/inventory/", json={
        "name": "Pasta",
        "quantity": 200.0,
        "unit": "g",
        "location": "pantry",
        "low_threshold": 100.0,
    })
    pasta_id = pasta.json()["id"]

    # Add sauce — below threshold
    sauce = await client.post("/api/inventory/", json={
        "name": "TomatoSauce",
        "quantity": 0.5,
        "unit": "jar",
        "location": "pantry",
        "low_threshold": 2.0,
    })
    sauce_id = sauce.json()["id"]

    # 2. Auto-generate from thresholds
    gen = await client.post("/api/shopping/generate-from-thresholds")
    assert gen.status_code == 200
    shopping = await client.get("/api/shopping/")
    names = [i["name"] for i in shopping.json()]
    assert "TomatoSauce" in names
    assert "Pasta" not in names  # Pasta is above threshold

    # 3. Create recipe: needs 300g pasta and 2 jars sauce
    recipe = await client.post("/api/recipes/", json={
        "name": "Pasta Marinara",
        "servings": 2,
        "tags": "dinner,italian",
        "ingredients": [
            {"name": "Pasta", "quantity": 300.0, "unit": "g", "inventory_id": pasta_id},
            {"name": "TomatoSauce", "quantity": 2.0, "unit": "jar", "inventory_id": sauce_id},
        ]
    })
    recipe_id = recipe.json()["id"]

    # 4. Add recipe to calendar on two separate days
    await client.post("/api/calendar/", json={
        "date": "2026-04-20",
        "meal_slot": "dinner",
        "recipe_id": recipe_id,
        "servings": 2,
    })
    await client.post("/api/calendar/", json={
        "date": "2026-04-22",
        "meal_slot": "dinner",
        "recipe_id": recipe_id,
        "servings": 2,
    })

    # 5. Generate weekly shopping starting Monday 2026-04-20
    weekly = await client.post("/api/calendar/generate-weekly-shopping?start=2026-04-20")
    assert weekly.status_code == 200
    items = weekly.json()["items"]

    # Pasta: have 200g, need 300g Mon + 300g Wed = 600g total
    # Mon: 300 - 200 = 100g shortfall; remaining stock = 0
    # Wed: 300 - 0 = 300g shortfall
    # Total pasta shortfall = 400g
    pasta_items = [i for i in items if i["name"] == "Pasta"]
    assert sum(i["quantity_needed"] for i in pasta_items) == pytest.approx(400.0)

    # Sauce: have 0.5 jar, need 2 Mon + 2 Wed = 4 total
    # Mon: 2 - 0.5 = 1.5 shortfall; remaining = 0
    # Wed: 2 - 0 = 2.0 shortfall
    # Total sauce shortfall = 3.5
    sauce_items = [i for i in items if i["name"] == "TomatoSauce"]
    assert sum(i["quantity_needed"] for i in sauce_items) == pytest.approx(3.5)
```

- [ ] **Step 2: Run integration test**

```bash
pytest tests/test_integration.py -v
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

```bash
pytest -v
```

Expected: All tests PASS with 0 failures.

- [ ] **Step 4: Commit**

```bash
git add tests/test_integration.py
git commit -m "test: add full end-to-end integration test for weekly meal planning flow"
```

---

## Future: Barcode Scanner (Deferred)

When ready to implement barcode scanning, install `python-barcode` and `pillow`. Add a `POST /api/inventory/lookup-barcode?code=<barcode>` endpoint that queries the Open Food Facts API (no auth required) for product name and returns a pre-filled `InventoryItemCreate`. The frontend can use the browser `BarcodeDetector` API (Chrome/Android) or a JS library like `quagga2` via CDN for camera access. The `barcode` column is already in the inventory schema.

---

## Self-Review

**Spec coverage check:**
- ✅ Track amounts, locations, expiration dates → inventory CRUD with all fields
- ✅ Simple, mobile-accessible UI → Alpine.js + Tailwind, bottom tab bar, modal sheets
- ✅ SQLite storage → `databases` + `aiosqlite`
- ✅ Shopping list based on low threshold → `services/shopping_service.py` + `POST /api/shopping/generate-from-thresholds`
- ✅ Barcode scanner → deferred, column reserved, scaffold documented
- ✅ Recipe integration → CRUD with ingredients, tag filtering, available-only filter
- ✅ Add recipe missing items to shopping list → `POST /api/recipes/{id}/add-to-shopping-list`
- ✅ Weekly meal calendar → `meal_calendar_table` + calendar router
- ✅ Weekly shopping from calendar accounting for prior days' usage → `calendar_service.py` simulated inventory depletion

**Placeholder scan:** None found — all steps have complete code.

**Type consistency check:**
- `InventoryItem`, `ShoppingListItem`, `Recipe`, `MealCalendarEntry` — used consistently throughout routers and tests.
- `_get_recipe_with_ingredients` returns consistent shape used in all recipe endpoints.
- `shopping_list_table` column names (`checked`, `source`, `inventory_id`) match across service and router.
