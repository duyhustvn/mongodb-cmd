use actix_web::{App, HttpResponse, HttpServer, Responder, get, web};
use dotenvy::dotenv;

use std::sync::Arc;
use tokio;

mod config;
mod get_profile_detail;
mod get_profiles;

struct AppState {
    // mongodb_client: Client,
}

#[get("/mongodb-cmd/_info")]
async fn health_check() -> impl Responder {
    HttpResponse::Ok().body("Ok")
}

#[tokio::main]
async fn main() -> std::io::Result<()> {
    dotenv().ok();

    let app_state = Arc::new(AppState {});
    println!("Server running on :8081");

    // Start HTTP Server
    HttpServer::new(move || {
        App::new()
            .app_data(web::Data::new(app_state.clone())) // Inject shared state
            .service(get_profiles::get_profiles)
            .service(health_check)
            .service(get_profile_detail::get_profile_detail)
    })
    .bind(("0.0.0.0", 8081))?
    .run()
    .await
}
