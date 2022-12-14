from typing import Any
from pydantic import BaseModel

class API(BaseModel):
    token: str
    api: Any