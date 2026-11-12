from fastapi import APIRouter, HTTPException, status
from tortoise.exceptions import IntegrityError

from app.config import ADMIN_USERNAME
from app.deps import AdminUser, CurrentUser
from app.models import Users
from app.schemas.user import Message, UserCreate, UserResponse
from app.security import hash_password

router = APIRouter()


@router.post(
    "",
    response_model=UserResponse,
    status_code=status.HTTP_201_CREATED,
    summary="Create a user (admin only)",
)
async def create_user(user_in: UserCreate, _: AdminUser) -> Users:
    try:
        return await Users.create(
            username=user_in.username, password_hash=hash_password(user_in.password)
        )
    except IntegrityError:
        raise HTTPException(status.HTTP_409_CONFLICT, "Username already exists") from None


@router.get("", response_model=list[UserResponse], summary="List users (admin only)")
async def get_users(_: AdminUser) -> list[Users]:
    return await Users.all().order_by("id")


@router.get("/me", response_model=UserResponse, summary="Get the current user")
async def get_me(user: CurrentUser) -> Users:
    return user


@router.get("/{username}", response_model=UserResponse, summary="Get a user (admin only)")
async def get_user(username: str, _: AdminUser) -> Users:
    user = await Users.get_or_none(username=username)
    if user is None:
        raise HTTPException(status.HTTP_404_NOT_FOUND, f"User {username} not found")
    return user


@router.delete("/{username}", response_model=Message, summary="Delete a user (admin only)")
async def delete_user(username: str, _: AdminUser) -> Message:
    if username == ADMIN_USERNAME:
        raise HTTPException(status.HTTP_400_BAD_REQUEST, "The admin user cannot be deleted")
    if not await Users.filter(username=username).delete():
        raise HTTPException(status.HTTP_404_NOT_FOUND, f"User {username} not found")
    return Message(message=f"Deleted user {username}")
