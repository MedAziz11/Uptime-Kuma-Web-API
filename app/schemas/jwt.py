from typing import Optional

from pydantic import BaseModel

class JWToken(BaseModel):
    access_token: str
    token_type: str = "Bearer"
    
    
class JWTData(BaseModel):
    sub: Optional[str]= None