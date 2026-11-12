from tortoise import fields, models


class Users(models.Model):
    """An API user. The schema is unchanged since v1 so existing databases keep working."""

    id = fields.IntField(primary_key=True)
    username = fields.CharField(max_length=20, unique=True)
    password_hash = fields.CharField(max_length=200, null=True)
    created_at = fields.DatetimeField(auto_now_add=True)
    last_visit = fields.DatetimeField(auto_now=True)

    class Meta:
        table = "users"
