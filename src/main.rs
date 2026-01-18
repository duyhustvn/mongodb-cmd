use actix_web::{App, HttpResponse, HttpServer, Responder, get, post, web};
use dotenvy::dotenv;
use futures::{TryStreamExt, stream::StreamExt};
use mongodb::{
    Collection,
    bson::{Document, doc},
    options::ClientOptions,
    options::FindOptions,
};
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tokio;

use crate::config::MongodbConfig;

mod config;

#[derive(Deserialize, Debug)]
#[serde(rename_all = "lowercase")]
enum OrderType {
    Asc,
    Desc,
}

#[derive(Deserialize)]
struct CountSystemProfileRequest {
    databases: Vec<String>,
}

#[derive(Serialize)]
struct CountSystemProfileResponse {
    results: Vec<CountSystemProfileResult>,
}

#[derive(Serialize)]
struct CountSystemProfileResult {
    url: String,
    database: String,
    count: u64,
    // only include error field if it exists
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

struct AppState {
    // mongodb_client: Client,
}

#[get("/mongodb-cmd/_info")]
async fn health_check() -> impl Responder {
    HttpResponse::Ok().body("Ok")
}

#[get("/mongodb-cmd/count-profile")]
async fn count_profile() -> impl Responder {
    let mongodb_config = MongodbConfig::new();

    let mut results = Vec::new();

    for url in mongodb_config.url {
        let options = ClientOptions::parse(&url)
            .await
            .expect("Failed to parse Mongodb url");
        let client = mongodb::Client::with_options(options).expect("Failed to init MongoDB client");
        let mut dbs = Vec::new();

        let parsed_url = url::Url::parse(&url).expect("Invalid URL");

        let host = parsed_url
            .host_str()
            .expect("Url must have a host")
            .to_string();

        match client.list_database_names().await {
            Ok(db_result) => dbs = db_result,
            Err(err) => {
                println!("Error while list database: {}", err);
                results.push(CountSystemProfileResult {
                    url: host.clone(),
                    database: String::new(),
                    count: 0,
                    error: Some(err.to_string()),
                });
                return HttpResponse::InternalServerError()
                    .json(CountSystemProfileResponse { results });
            }
        }

        let collection_name = "system.profile";

        for db_name in dbs {
            let db = client.database(db_name.as_str());
            let profiles: Collection<Document> = db.collection(collection_name);

            match profiles.count_documents(doc! {}).await {
                Ok(count) => {
                    results.push(CountSystemProfileResult {
                        url: host.clone(),
                        database: db_name.to_string(),
                        count,
                        error: None,
                    });
                }
                Err(err) => {
                    results.push(CountSystemProfileResult {
                        url: host.clone(),
                        database: db_name.to_string(),
                        count: 0,
                        error: Some(err.to_string()),
                    });
                }
            };
        }
    }

    HttpResponse::Ok().json(CountSystemProfileResponse { results })
}

#[derive(Deserialize)]
struct GetDatabaseProfileQueryString {
    endpoint: String,
    database: String,
    limit: Option<i64>,
    collection: Option<String>,
    duration: Option<i64>,
    offset: Option<u64>,
    order_by: Option<String>,
    order_type: Option<OrderType>,
}

#[get("/mongodb-cmd/profile")]
async fn get_profile(
    data: web::Data<Arc<AppState>>,
    query: web::Query<GetDatabaseProfileQueryString>,
) -> impl Responder {
    let mongodb_config = MongodbConfig::new();
    let mut url = String::new();

    for mongodb_url in mongodb_config.url {
        println!("mongodb_url: {}", mongodb_url);
        println!("endpoint: {}", &query.endpoint);
        if mongodb_url.contains(&query.endpoint) {
            url = mongodb_url.clone();
            break;
        }
    }

    if url.is_empty() {
        return HttpResponse::BadRequest().body("Invalid url");
    }
    println!("url: {}", url);

    let options = ClientOptions::parse(&url)
        .await
        .expect("Failed to parse Mongodb url");

    let client = mongodb::Client::with_options(options).expect("Failed to init MongoDB client");
    let db = client.database(&query.database);
    let col: Collection<Document> = db.collection("system.profile");

    let limit = query.limit.unwrap_or(20); // Default to 20 if not provided
    let offset = query.offset.unwrap_or(0); // Default to 0 if not provided
    let order_by = query.order_by.as_deref().unwrap_or("ts");
    let order_type = match query.order_type {
        Some(OrderType::Asc) => 1,
        Some(OrderType::Desc) => -1,
        None => -1, // default to desc
    };

    let find_options = FindOptions::builder()
        .sort(doc! {order_by: order_type})
        .limit(limit)
        .skip(offset)
        .build();

    let mut filter = doc! {};

    if let Some(duration_ms) = query.duration {
        filter.insert("millis", doc! {"$gt": duration_ms});
    }

    if let Some(collection_name) = &query.collection {
        let namespace = format!("{}.{}", query.database, collection_name);
        filter.insert("ns", namespace);
    }

    let mut cursor = match col.find(filter).with_options(find_options).await {
        Ok(cursor) => cursor,
        Err(err) => return HttpResponse::InternalServerError().body(err.to_string()),
    };

    let mut documents = Vec::new();
    while let Ok(Some(doc)) = cursor.try_next().await {
        documents.push(doc);
    }

    HttpResponse::Ok().json(documents)
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
            .service(count_profile)
            .service(health_check)
    })
    .bind(("0.0.0.0", 8081))?
    .run()
    .await
}
