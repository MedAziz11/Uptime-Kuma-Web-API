from pydantic import BaseModel


class Tag(BaseModel):
    name: str
    color: str


class TagUpdate(BaseModel):
    name: str | None = None
    color: str | None = None
