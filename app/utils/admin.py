from models.user import Users
from utils.security import hash_password
from config import settings

async def check_admin():
    admin_obj = await Users.get_or_none(username="admin")
    if not admin_obj:
        admin_obj = await Users.create(username="admin", password_hash=hash_password(settings.ADMIN_PASSWORD))
        
    return admin_obj