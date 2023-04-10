from tortoise import fields, models
from tortoise.contrib.pydantic import pydantic_model_creator


class Users(models.Model):
    """The User model."""

    id = fields.IntField(pk=True, auto_generated=True)
    username = fields.CharField(max_length=20, unique=True)
    password_hash = fields.CharField(max_length=200, null=True)
    created_at = fields.DatetimeField(auto_now_add=True)
    last_visit = fields.DatetimeField(auto_now=True)

    class PydanticMeta:
        exclude = ["password_hash"]


UserResponse = pydantic_model_creator(Users, name="User")
UserCreate = pydantic_model_creator(Users, name="UserIn", exclude_readonly=True)
