from typing import Any, List

from fastapi import APIRouter, Depends, HTTPException
from tortoise.contrib.fastapi import HTTPNotFoundError
from schemas.api import API
from utils.deps import get_current_user

from models.user import Users, UserResponse
from schemas.user import RegisterUser
from utils.security import hash_password


router = APIRouter(redirect_slashes=True)

@router.post('', response_model = UserResponse)
async def create_user(user_in: RegisterUser)-> Any:
    """ Sign up """
    user = await Users.get_or_none(username=user_in.username)
    if user:
        raise HTTPException(
            status_code = 400,
            detail= 'Username already exists'
        )

    user = await Users.create(username= user_in.username, password_hash=hash_password(user_in.password))
    return await UserResponse.from_tortoise_orm(user)


@router.get("", response_model=List[UserResponse])
async def get_users(cur_user: API = Depends(get_current_user)):
    return await UserResponse.from_queryset(Users.all())


@router.get("/{username}", response_model=UserResponse, responses={404: {"model": HTTPNotFoundError}})
async def get_user(username: str, cur_user: API = Depends(get_current_user)):
    return await UserResponse.from_queryset_single(Users.get(username=username))


@router.delete("/{username}", responses={404: {"model": HTTPNotFoundError}})
async def delete_user(username: str, cur_user: API = Depends(get_current_user)):
    deleted_count = await Users.filter(username=username).delete()
    if not deleted_count:
        raise HTTPException(status_code=404, detail=f"User {username} not found")
    return {"message": f"Deleted user {username}"}